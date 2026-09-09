package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const sharedTaskSequenceMigrationMaxRows = 256

type retainedTaskSequence struct {
	projectCode string
	maxNumber   uint64
}

func sharedTaskSequenceMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	migration := migrate.Migration{Version: sharedTaskSequenceMigrationVersion, Name: sharedTaskSequenceMigrationName}
	rows, err := db.Query(ctx, "SELECT id,revision,payload FROM shared_tasks ORDER BY id")
	if err != nil {
		return migration, fmt.Errorf("read retained Tasks for sequence reconciliation: %w", err)
	}
	if len(rows.Rows) == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
		return migration, nil
	}
	if len(rows.Rows) > sharedTaskSequenceMigrationMaxRows {
		return migration, fmt.Errorf("current Task inventory exceeds bounded sequence reconciliation maximum %d", sharedTaskSequenceMigrationMaxRows)
	}

	projects := make(map[string]retainedTaskSequence)
	for _, row := range rows.Rows {
		if len(row) != 3 {
			return migration, fmt.Errorf("invalid retained Task row shape for sequence reconciliation")
		}
		id, idOK := row[0].(string)
		storeRevision, revisionOK := row[1].(int64)
		payload, payloadOK := row[2].([]byte)
		if !idOK || id == "" || !revisionOK || storeRevision < 1 || !payloadOK {
			return migration, fmt.Errorf("invalid retained Task row for sequence reconciliation")
		}
		var task model.TaskAuthoring
		if err := json.Unmarshal(payload, &task); err != nil {
			return migration, fmt.Errorf("decode retained Task %s for sequence reconciliation: %w", id, err)
		}
		if task.ID != id || task.Revision != int(storeRevision) {
			return migration, fmt.Errorf("retained Task %s identity/revision mismatch", id)
		}
		if err := model.ValidateTaskAuthoringRevision(task, true); err != nil {
			return migration, fmt.Errorf("retained Task %s validation failed during sequence reconciliation: %w", id, err)
		}
		projectCode, number, err := model.ParseTaskID(id)
		if err != nil {
			return migration, fmt.Errorf("retained Task %s has invalid canonical ID: %w", id, err)
		}
		if err := model.ValidateProjectCode(projectCode); err != nil {
			return migration, fmt.Errorf("retained Task %s has invalid project code: %w", id, err)
		}
		if existing, ok := projects[task.ProjectID]; ok {
			if existing.projectCode != projectCode {
				return migration, fmt.Errorf("project %s has inconsistent Task project codes %q and %q", task.ProjectID, existing.projectCode, projectCode)
			}
			if number > existing.maxNumber {
				existing.maxNumber = number
				projects[task.ProjectID] = existing
			}
			continue
		}
		projects[task.ProjectID] = retainedTaskSequence{
			projectCode: projectCode,
			maxNumber:   number,
		}
	}

	projectIDs := make([]string, 0, len(projects))
	for projectID := range projects {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Strings(projectIDs)
	for _, projectID := range projectIDs {
		sequence := projects[projectID]
		if sequence.maxNumber >= model.MaxSafeInteger {
			return migration, fmt.Errorf("project %s Task sequence overflows maximum", projectID)
		}
		requiredNext := sequence.maxNumber + 1
		rows, err := db.Query(ctx, `SELECT project_code,next_number FROM shared_entity_sequences WHERE entity_type='task' AND project_id=?`, projectID)
		if err != nil {
			return migration, fmt.Errorf("read Task sequence for project %s: %w", projectID, err)
		}
		if len(rows.Rows) > 1 {
			return migration, fmt.Errorf("duplicate Task sequence rows for project %s", projectID)
		}
		if len(rows.Rows) == 0 {
			migration.Statements = append(migration.Statements, upstream.Statement{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES('task',?,?,?)`, Args: []any{projectID, sequence.projectCode, int64(requiredNext)}, RequireRowsAffected: 1})
			continue
		}
		if len(rows.Rows[0]) != 2 {
			return migration, fmt.Errorf("invalid Task sequence row shape for project %s", projectID)
		}
		projectCode, codeOK := rows.Rows[0][0].(string)
		nextNumber, numberOK := rows.Rows[0][1].(int64)
		if !codeOK || projectCode != sequence.projectCode {
			return migration, fmt.Errorf("Task sequence project code mismatch for project %s", projectID)
		}
		if !numberOK || nextNumber < 1 || uint64(nextNumber) > model.MaxSafeInteger {
			return migration, fmt.Errorf("invalid Task sequence number for project %s", projectID)
		}
		if uint64(nextNumber) < requiredNext {
			migration.Statements = append(migration.Statements, upstream.Statement{SQL: `UPDATE shared_entity_sequences SET next_number=? WHERE entity_type='task' AND project_id=? AND project_code=? AND next_number=?`, Args: []any{int64(requiredNext), projectID, sequence.projectCode, nextNumber}, RequireRowsAffected: 1})
		}
	}
	if len(migration.Statements) == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
	}
	return migration, nil
}
