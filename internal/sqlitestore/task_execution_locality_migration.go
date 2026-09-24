package sqlitestore

import (
	"context"
	"fmt"
	"reflect"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

const (
	taskExecutionLocalityMigrationID = "task_execution_shared_to_local"
	taskExecutionLocalityMaxRows     = 4096
	taskExecutionLocalityBatchRows   = 128
)

type taskExecutionTableMigration struct {
	sharedTable string
	localTable  string
	columns     string
	order       string
}

var taskExecutionTables = []taskExecutionTableMigration{
	{sharedTable: "shared_task_execution_states", localTable: "local_task_execution_states", columns: "task_id,project_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at", order: "project_id,task_id"},
	{sharedTable: "shared_task_execution_phases", localTable: "local_task_execution_phases", columns: "id,task_id,project_id,execution_revision,stage,status,head_sha,branch,task_revision_sha256,event_kind,decision,comment,created_at", order: "id"},
	{sharedTable: "shared_task_execution_verifications", localTable: "local_task_execution_verifications", columns: "id,project_id,task_id,operation_id,attempt_revision,outcome,receipt_json,created_at", order: "id"},
}

func (d *Databases) MigrateTaskExecutionStateToLocal(ctx context.Context) error {
	if d == nil || d.Shared == nil || d.Local == nil {
		return fmt.Errorf("Shared and Local stores are required for TaskExecution migration")
	}
	state, err := d.localUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID)
	if err != nil {
		return err
	}
	sharedState, err := d.sharedUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID)
	if err != nil {
		return err
	}
	exists := make([]bool, len(taskExecutionTables))
	existingCount := 0
	for i, table := range taskExecutionTables {
		exists[i], err = d.sharedTableExists(ctx, table.sharedTable)
		if err != nil {
			return err
		}
		if exists[i] {
			existingCount++
		}
	}
	if existingCount != 0 && existingCount != len(taskExecutionTables) {
		return fmt.Errorf("Shared TaskExecution source tables are incomplete")
	}
	if existingCount == 0 {
		if sharedState != "copied" && sharedState != "complete" {
			return fmt.Errorf("Shared TaskExecution source is missing without a durable cutover marker")
		}
		return d.finishTaskExecutionLocalityMigration(ctx)
	}
	if state == "complete" || sharedState == "complete" {
		for _, table := range taskExecutionTables {
			rows, queryErr := d.Shared.Query(ctx, "SELECT COUNT(*) FROM "+table.sharedTable)
			if queryErr != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 || rows.Rows[0][0] != int64(0) {
				return fmt.Errorf("completed TaskExecution migration has unexpected Shared source data")
			}
		}
		return d.dropSharedTaskExecutionTables(ctx)
	}
	for _, table := range taskExecutionTables {
		if err := d.copyTaskExecutionTable(ctx, table); err != nil {
			return err
		}
	}
	if err := d.setLocalUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID, "copied"); err != nil {
		return err
	}
	if err := d.setSharedUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID, "copied"); err != nil {
		return err
	}
	if err := d.dropSharedTaskExecutionTables(ctx); err != nil {
		return err
	}
	return d.finishTaskExecutionLocalityMigration(ctx)
}

func (d *Databases) copyTaskExecutionTable(ctx context.Context, table taskExecutionTableMigration) error {
	query := "SELECT " + table.columns + " FROM " + table.sharedTable + " ORDER BY " + table.order + " LIMIT ?"
	source, err := d.Shared.Query(ctx, query, taskExecutionLocalityMaxRows+1)
	if err != nil {
		return fmt.Errorf("read %s for Local migration: %w", table.sharedTable, err)
	}
	if len(source.Rows) > taskExecutionLocalityMaxRows {
		return fmt.Errorf("%s exceeds bounded TaskExecution migration maximum %d", table.sharedTable, taskExecutionLocalityMaxRows)
	}
	if err := validateTaskExecutionMigrationRows(table, source.Rows); err != nil {
		return err
	}
	placeholders := make([]byte, stringsCount(table.columns, ',')+1)
	for i := range placeholders {
		placeholders[i] = '?'
	}
	insert := "INSERT OR IGNORE INTO " + table.localTable + "(" + table.columns + ") VALUES(" + joinQuestionMarks(len(placeholders)) + ")"
	for start := 0; start < len(source.Rows); start += taskExecutionLocalityBatchRows {
		end := start + taskExecutionLocalityBatchRows
		if end > len(source.Rows) {
			end = len(source.Rows)
		}
		statements := make([]upstream.Statement, 0, end-start)
		for _, row := range source.Rows[start:end] {
			if len(row) != len(placeholders) {
				return fmt.Errorf("invalid %s migration row shape", table.sharedTable)
			}
			statements = append(statements, upstream.Statement{SQL: insert, Args: append([]any(nil), row...)})
		}
		if len(statements) > 0 {
			if _, err := d.Local.Batch(ctx, statements); err != nil {
				return fmt.Errorf("copy %s to Local: %w", table.sharedTable, err)
			}
		}
	}
	local, err := d.Local.Query(ctx, "SELECT "+table.columns+" FROM "+table.localTable+" ORDER BY "+table.order+" LIMIT ?", taskExecutionLocalityMaxRows+1)
	if err != nil {
		return fmt.Errorf("verify Local %s migration: %w", table.localTable, err)
	}
	if !reflect.DeepEqual(local.Rows, source.Rows) {
		return fmt.Errorf("Local %s differs from Shared migration source", table.localTable)
	}
	return nil
}

func validateTaskExecutionMigrationRows(table taskExecutionTableMigration, rows [][]any) error {
	switch table.sharedTable {
	case "shared_task_execution_states":
		_, err := decodeTaskExecutionStateRows(rows)
		return err
	case "shared_task_execution_phases":
		for _, row := range rows {
			if len(row) != 13 {
				return fmt.Errorf("invalid Shared TaskExecution phase row")
			}
			normalized := append([]any(nil), row...)
			for _, index := range []int{10, 11} {
				if normalized[index] == nil {
					normalized[index] = ""
				}
			}
			if _, err := decodeTaskExecutionPhaseRow(normalized); err != nil {
				return err
			}
		}
	case "shared_task_execution_verifications":
		for _, row := range rows {
			if len(row) != 8 {
				return fmt.Errorf("invalid Shared TaskExecution verification row")
			}
			projectID, projectOK := row[1].(string)
			taskID, taskOK := row[2].(string)
			operationID, operationOK := row[3].(string)
			attemptRevision, revisionOK := row[4].(int64)
			outcome, outcomeOK := row[5].(string)
			receiptJSON, receiptOK := row[6].(string)
			createdAtText, createdAtOK := row[7].(string)
			if !(projectOK && taskOK && operationOK && revisionOK && outcomeOK && receiptOK && createdAtOK) {
				return fmt.Errorf("invalid Shared TaskExecution verification value types")
			}
			receipt, err := decodeTaskExecutionVerification(receiptJSON)
			if err != nil {
				return err
			}
			createdAt, err := time.Parse(time.RFC3339Nano, createdAtText)
			if err != nil || receipt.ProjectID != projectID || receipt.TaskID != taskID || receipt.OperationID != operationID || int64(receipt.AttemptRevision) != attemptRevision || receipt.Outcome != outcome || !receipt.CompletedAt.Equal(createdAt) {
				return fmt.Errorf("Shared TaskExecution verification row contradicts its receipt")
			}
		}
	default:
		return fmt.Errorf("unregistered Shared TaskExecution migration table %q", table.sharedTable)
	}
	return nil
}

func (d *Databases) sharedTableExists(ctx context.Context, table string) (bool, error) {
	rows, err := d.Shared.Query(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return false, fmt.Errorf("invalid Shared TaskExecution table inspection")
	}
	count, ok := rows.Rows[0][0].(int64)
	if !ok || count < 0 || count > 1 {
		return false, fmt.Errorf("invalid Shared TaskExecution table count")
	}
	return count == 1, nil
}

func (d *Databases) sharedUpgradeMigrationState(ctx context.Context, migrationID string) (string, error) {
	rows, err := d.Shared.Query(ctx, `SELECT state FROM shared_upgrade_migrations WHERE migration_id=?`, migrationID)
	if err != nil {
		return "", err
	}
	if len(rows.Rows) == 0 {
		return "", nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return "", fmt.Errorf("invalid Shared upgrade migration marker")
	}
	state, ok := rows.Rows[0][0].(string)
	if !ok || state != "copied" && state != "complete" {
		return "", fmt.Errorf("invalid Shared TaskExecution migration state")
	}
	return state, nil
}

func (d *Databases) setSharedUpgradeMigrationState(ctx context.Context, migrationID, state string) error {
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_upgrade_migrations(migration_id,state,updated_at) VALUES(?,?,?) ON CONFLICT(migration_id) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at`, migrationID, state, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (d *Databases) localUpgradeMigrationState(ctx context.Context, migrationID string) (string, error) {
	rows, err := d.Local.Query(ctx, `SELECT state FROM local_upgrade_migrations WHERE migration_id=?`, migrationID)
	if err != nil {
		return "", err
	}
	if len(rows.Rows) == 0 {
		return "", nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return "", fmt.Errorf("invalid Local upgrade migration marker")
	}
	state, ok := rows.Rows[0][0].(string)
	if !ok || state != "copied" && state != "in_progress" && state != "complete" {
		return "", fmt.Errorf("invalid Local upgrade migration state")
	}
	return state, nil
}

func (d *Databases) setLocalUpgradeMigrationState(ctx context.Context, migrationID, state string) error {
	_, err := d.Local.Exec(ctx, `INSERT INTO local_upgrade_migrations(migration_id,state,updated_at) VALUES(?,?,?) ON CONFLICT(migration_id) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at`, migrationID, state, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (d *Databases) dropSharedTaskExecutionTables(ctx context.Context) error {
	_, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `DROP TABLE IF EXISTS shared_task_execution_verifications`},
		{SQL: `DROP TABLE IF EXISTS shared_task_execution_phases`},
		{SQL: `DROP TABLE IF EXISTS shared_task_execution_states`},
	})
	return err
}

func (d *Databases) finishTaskExecutionLocalityMigration(ctx context.Context) error {
	if err := d.dropSharedTaskExecutionTables(ctx); err != nil {
		return err
	}
	if err := d.setSharedUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID, "complete"); err != nil {
		return err
	}
	return d.setLocalUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID, "complete")
}

func stringsCount(value string, target byte) int {
	count := 0
	for i := range value {
		if value[i] == target {
			count++
		}
	}
	return count
}

func joinQuestionMarks(count int) string {
	if count < 1 {
		return ""
	}
	result := "?"
	for i := 1; i < count; i++ {
		result += ",?"
	}
	return result
}
