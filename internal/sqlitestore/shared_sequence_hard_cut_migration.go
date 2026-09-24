package sqlitestore

import (
	"context"
	"fmt"
	"strings"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	sharedSequenceHardCutMigrationID = "shared_legacy_sequences_to_entity_sequences_v1"
	sharedSequenceHardCutMaxRows     = 4096
	sharedSequenceHardCutBatchSize   = 128
)

type legacySharedSequence struct {
	entityType  string
	projectID   string
	projectCode string
	next        int64
}

func (d *Databases) MigrateLegacySharedSequences(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for sequence migration")
	}
	state, err := d.sharedUpgradeMigrationState(ctx, sharedSequenceHardCutMigrationID)
	if err != nil {
		return err
	}
	if state == "complete" {
		return nil
	}
	if err := d.setSharedUpgradeMigrationState(ctx, sharedSequenceHardCutMigrationID, "in_progress"); err != nil {
		return err
	}
	legacy := make([]legacySharedSequence, 0)
	for _, source := range []struct {
		table      string
		entityType string
		column     string
	}{
		{"shared_task_sequences", "task", "next_task_number"},
		{"shared_adr_sequences", "adr", "next_adr_number"},
	} {
		exists, err := d.sharedTableExists(ctx, source.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		rows, err := d.Shared.Query(ctx, fmt.Sprintf("SELECT project_id,project_code,%s FROM %s ORDER BY project_id LIMIT ?", source.column, source.table), int64(sharedSequenceHardCutMaxRows+1))
		if err != nil {
			return err
		}
		if len(rows.Rows) > sharedSequenceHardCutMaxRows {
			return fmt.Errorf("legacy %s sequence migration exceeds bounded row maximum", source.entityType)
		}
		for _, row := range rows.Rows {
			if len(row) != 3 {
				return fmt.Errorf("invalid legacy %s sequence row", source.entityType)
			}
			projectID, projectOK := row[0].(string)
			projectCode, codeOK := row[1].(string)
			next, nextOK := row[2].(int64)
			if !projectOK || !codeOK || !nextOK || model.ValidateProjectIdentifier(projectID) != nil || model.ValidateProjectCode(projectCode) != nil || next < 1 || uint64(next) > model.MaxSafeInteger {
				return fmt.Errorf("invalid legacy %s sequence value", source.entityType)
			}
			legacy = append(legacy, legacySharedSequence{
				entityType:  source.entityType,
				projectID:   projectID,
				projectCode: projectCode,
				next:        next,
			})
		}
	}
	columns, err := d.sharedProjectIdentifierColumns(ctx)
	if err != nil {
		return err
	}
	identifierSequences := []struct {
		entityType string
		column     string
	}{
		{"task", "next_task_number"},
		{"adr", "next_adr_number"},
		{"rule", "next_rule_number"},
		{"journal", "next_journal_number"},
	}
	selectColumns := []string{"project_id", "project_code"}
	for _, sequence := range identifierSequences {
		if columns[sequence.column] {
			selectColumns = append(selectColumns, sequence.column)
		}
	}
	columnIndexes := make(map[string]int, len(selectColumns))
	for index, column := range selectColumns {
		columnIndexes[column] = index
	}
	query := "SELECT " + strings.Join(selectColumns, ",") + " FROM shared_project_identifiers ORDER BY project_id LIMIT ?"
	rows, err := d.Shared.Query(ctx, query, int64(sharedSequenceHardCutMaxRows+1))
	if err != nil {
		return err
	}
	if len(rows.Rows) > sharedSequenceHardCutMaxRows {
		return fmt.Errorf("project identifier sequence migration exceeds bounded row maximum")
	}
	for _, row := range rows.Rows {
		if len(row) != len(selectColumns) {
			return fmt.Errorf("invalid project identifier sequence row")
		}
		projectID, projectOK := row[0].(string)
		projectCode, codeOK := row[1].(string)
		if !projectOK || !codeOK || model.ValidateProjectIdentifier(projectID) != nil || model.ValidateProjectCode(projectCode) != nil {
			return fmt.Errorf("invalid project identifier sequence identity")
		}
		for _, sequence := range identifierSequences {
			columnIndex, selected := columnIndexes[sequence.column]
			if !selected {
				continue
			}
			next, ok := row[columnIndex].(int64)
			if !ok || next < 1 || uint64(next) > model.MaxSafeInteger {
				return fmt.Errorf("invalid project %s sequence value", sequence.entityType)
			}
			legacy = append(legacy, legacySharedSequence{
				entityType:  sequence.entityType,
				projectID:   projectID,
				projectCode: projectCode,
				next:        next,
			})
		}
	}
	statements := make([]upstream.Statement, 0, len(legacy)+1)
	for _, sequence := range legacy {
		if _, ok := sharedLifecycle(sequence.entityType); !ok {
			return fmt.Errorf("unsupported legacy Shared sequence family %q", sequence.entityType)
		}
		statements = append(statements, upstream.Statement{
			SQL:  `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES(?,?,?,?) ON CONFLICT(entity_type,project_id) DO UPDATE SET next_number=MAX(shared_entity_sequences.next_number,excluded.next_number) WHERE shared_entity_sequences.project_code=excluded.project_code`,
			Args: []any{sequence.entityType, sequence.projectID, sequence.projectCode, sequence.next}, RequireRowsAffected: 1,
		})
	}
	for offset := 0; offset < len(statements); offset += sharedSequenceHardCutBatchSize {
		end := offset + sharedSequenceHardCutBatchSize
		if end > len(statements) {
			end = len(statements)
		}
		if _, err := d.Shared.Batch(ctx, statements[offset:end]); err != nil {
			return fmt.Errorf("reconcile canonical Shared sequences: %w", err)
		}
	}
	if _, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `DROP TABLE IF EXISTS shared_task_sequences`},
		{SQL: `DROP TABLE IF EXISTS shared_adr_sequences`},
		{SQL: `DELETE FROM shared_entity_sequences WHERE entity_type='train'`},
	}); err != nil {
		return fmt.Errorf("remove retired Shared sequence sources: %w", err)
	}
	if len(columns) > 2 {
		if err := d.rebuildSharedProjectIdentifiers(ctx); err != nil {
			return err
		}
	}
	return d.setSharedUpgradeMigrationState(ctx, sharedSequenceHardCutMigrationID, "complete")
}

func (d *Databases) sharedProjectIdentifierColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := d.Shared.Query(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, "shared_project_identifiers")
	if err != nil {
		return nil, err
	}
	columns := make(map[string]bool, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 1 {
			return nil, fmt.Errorf("invalid Shared project identifier schema row")
		}
		name, ok := row[0].(string)
		if !ok {
			return nil, fmt.Errorf("invalid Shared project identifier schema column")
		}
		columns[name] = true
	}
	if !columns["project_id"] || !columns["project_code"] {
		return nil, fmt.Errorf("Shared project identifier identity columns are unavailable")
	}
	for name := range columns {
		switch name {
		case "project_id", "project_code", "next_task_number", "next_adr_number", "next_rule_number", "next_journal_number", "next_train_number":
		default:
			return nil, fmt.Errorf("unsupported Shared project identifier column %q", name)
		}
	}
	return columns, nil
}

func (d *Databases) rebuildSharedProjectIdentifiers(ctx context.Context) error {
	if _, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `DROP TABLE IF EXISTS shared_project_identifiers_hard_cut`},
		{SQL: `CREATE TABLE shared_project_identifiers_hard_cut (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL)`},
		{SQL: `INSERT INTO shared_project_identifiers_hard_cut(project_id,project_code) SELECT project_id,project_code FROM shared_project_identifiers`},
		{SQL: `DROP TABLE shared_project_identifiers`},
		{SQL: `ALTER TABLE shared_project_identifiers_hard_cut RENAME TO shared_project_identifiers`},
	}); err != nil {
		return fmt.Errorf("remove retired Shared project identifier counters: %w", err)
	}
	return nil
}
