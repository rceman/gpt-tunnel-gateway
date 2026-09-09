package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

var retainedTaskSummaryIDs = []string{
	"GTW-TSK384", "GTW-TSK393", "GTW-TSK409", "GTW-TSK433", "GTW-TSK434", "GTW-TSK435", "GTW-TSK455", "GTW-TSK478", "GTW-TSK479", "GTW-TSK480", "GTW-TSK507", "GTW-TSK511", "GTW-TSK521", "GTW-TSK522", "GTW-TSK523", "GTW-TSK527", "GTW-TSK529", "GTW-TSK530", "GTW-TSK531", "GTW-TSK532", "GTW-TSK539", "GTW-TSK540", "GTW-TSK541", "GTW-TSK542", "GTW-TSK543", "GTW-TSK544", "GTW-TSK545",
}

func sharedTaskSummaryMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	migration := migrate.Migration{Version: sharedTaskSummaryMigrationVersion, Name: sharedTaskSummaryMigrationName}
	rows, err := db.Query(ctx, "SELECT COUNT(*) FROM shared_tasks")
	if err != nil {
		return migration, fmt.Errorf("count Shared Tasks for summary migration: %w", err)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return migration, fmt.Errorf("invalid Shared Task count result")
	}
	count, ok := rows.Rows[0][0].(int64)
	if !ok || count < 0 {
		return migration, fmt.Errorf("invalid Shared Task count %T", rows.Rows[0][0])
	}
	if count == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
		return migration, nil
	}
	if count != int64(len(retainedTaskSummaryIDs)) {
		return migration, fmt.Errorf("retained Task summary migration requires exactly %d current Tasks, found %d", len(retainedTaskSummaryIDs), count)
	}
	for _, id := range retainedTaskSummaryIDs {
		rows, err := db.Query(ctx, `SELECT id,revision,payload FROM shared_tasks WHERE id=?`, id)
		if err != nil {
			return migration, fmt.Errorf("read retained Task %s: %w", id, err)
		}
		if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
			return migration, fmt.Errorf("retained Task %s is missing or duplicated", id)
		}
		storeRevision, ok := rows.Rows[0][1].(int64)
		payload, payloadOK := rows.Rows[0][2].([]byte)
		if !ok || storeRevision < 1 || !payloadOK {
			return migration, fmt.Errorf("retained Task %s has invalid store row", id)
		}
		var current model.TaskAuthoring
		if err := json.Unmarshal(payload, &current); err != nil {
			return migration, fmt.Errorf("decode retained Task %s: %w", id, err)
		}
		if current.ID != id || current.Revision != int(storeRevision) || current.Summary != "" {
			return migration, fmt.Errorf("retained Task %s identity/revision/summary mismatch", id)
		}
		if err := model.ValidateTaskAuthoringRevision(current, true); err != nil {
			return migration, fmt.Errorf("retained Task %s validation failed: %w", id, err)
		}
		if len([]rune(current.Title)) > 128 {
			return migration, fmt.Errorf("retained Task %s title exceeds 128 characters", id)
		}
		summary := strings.TrimSpace(current.Title)
		if summary == "" || len([]rune(summary)) > 256 {
			return migration, fmt.Errorf("retained Task %s has no clear bounded summary", id)
		}
		current.Summary = summary
		oldRecordedAt := current.CreatedAt.UTC().Format(time.RFC3339Nano)
		oldHistory, err := db.Query(ctx, `SELECT project_id,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=? AND revision=?`, id, storeRevision)
		if err != nil {
			return migration, fmt.Errorf("read retained Task %s history: %w", id, err)
		}
		if len(oldHistory.Rows) > 1 {
			return migration, fmt.Errorf("retained Task %s has duplicate current history", id)
		}
		statements := make([]upstream.Statement, 0, 4)
		if len(oldHistory.Rows) == 0 {
			statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"task", id, current.ProjectID, storeRevision, "create", current.CreatedBy, "create", []byte(`["create"]`), payload, oldRecordedAt}, RequireRowsAffected: 1})
		} else {
			historyPayload, payloadOK := oldHistory.Rows[0][5].([]byte)
			if len(oldHistory.Rows[0]) != 7 || oldHistory.Rows[0][0] != current.ProjectID || !payloadOK || !bytes.Equal(historyPayload, payload) {
				return migration, fmt.Errorf("retained Task %s current history is not an exact preserved payload", id)
			}
		}
		current.Revision++
		current.UpdatedAt = time.Now().UTC()
		current.RevisionSHA256 = ""
		current.RevisionSHA256, err = model.HashTaskAuthoring(current)
		if err != nil {
			return migration, fmt.Errorf("hash retained Task %s: %w", id, err)
		}
		if err := model.ValidateTaskAuthoring(current); err != nil {
			return migration, fmt.Errorf("migrated Task %s validation failed: %w", id, err)
		}
		newPayload, err := json.Marshal(current)
		if err != nil {
			return migration, err
		}
		recordedAt := current.UpdatedAt.Format(time.RFC3339Nano)
		opID := "task-summary-migration-" + id + "-r" + fmt.Sprint(current.Revision)
		changedFields := []byte(`["summary"]`)
		statements = append(statements,
			upstream.Statement{SQL: `UPDATE shared_tasks SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{current.Revision, newPayload, recordedAt, id, storeRevision}, RequireRowsAffected: 1},
			upstream.Statement{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"task", id, current.ProjectID, current.Revision, "migration", "system:task-summary-migration", "add Task summary", changedFields, newPayload, recordedAt}, RequireRowsAffected: 1},
			upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{opID, "task", id, current.Revision, "task-summary-migration", newPayload, recordedAt}, RequireRowsAffected: 1},
		)
		migration.Statements = append(migration.Statements, statements...)
	}
	return migration, nil
}
