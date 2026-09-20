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

const sharedTaskPriorityMigrationMaxRows = 256

type taskPriorityAuditClassification struct {
	Priority  string
	Rationale string
}

var explicitTaskPriorityAuditClassifications = map[string]taskPriorityAuditClassification{
	"critical": {Priority: model.TaskPriorityP0, Rationale: "Priority re-audit explicitly classifies legacy critical work as P0."},
	"high":     {Priority: model.TaskPriorityP1, Rationale: "Priority re-audit explicitly classifies legacy high work as P1."},
	"medium":   {Priority: model.TaskPriorityP2, Rationale: "Priority re-audit explicitly classifies legacy medium work as P2."},
	"low":      {Priority: model.TaskPriorityP3, Rationale: "Priority re-audit explicitly classifies legacy low work as P3."},
}

func sharedTaskPriorityMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	migration := migrate.Migration{Version: sharedTaskPriorityMigrationVersion, Name: sharedTaskPriorityMigrationName}
	rows, err := db.Query(ctx, "SELECT id,revision,payload FROM shared_tasks ORDER BY id")
	if err != nil {
		return migration, fmt.Errorf("count Shared Tasks for priority re-audit: %w", err)
	}
	if len(rows.Rows) == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
		return migration, nil
	}
	if len(rows.Rows) > sharedTaskPriorityMigrationMaxRows {
		return migration, fmt.Errorf("current Task inventory exceeds bounded priority re-audit maximum %d", sharedTaskPriorityMigrationMaxRows)
	}
	migrationTime := time.Now().UTC()
	for _, row := range rows.Rows {
		if len(row) != 3 {
			return migration, fmt.Errorf("invalid current Task row shape")
		}
		id, idOK := row[0].(string)
		storeRevision, revisionOK := row[1].(int64)
		payload, payloadOK := row[2].([]byte)
		if !idOK || id == "" || !revisionOK || storeRevision < 1 || !payloadOK {
			return migration, fmt.Errorf("retained Task %s has invalid store row", id)
		}
		var current model.TaskAuthoring
		if err := json.Unmarshal(payload, &current); err != nil {
			return migration, fmt.Errorf("decode retained Task %s: %w", id, err)
		}
		if current.ID != id || current.Revision != int(storeRevision) {
			return migration, fmt.Errorf("current Task %s identity/revision mismatch", id)
		}
		if current.Status == model.TaskAuthoringDone || current.Status == model.TaskAuthoringArchived {
			continue
		}
		if err := model.ValidateTaskAuthoringRevision(current, true); err != nil {
			return migration, fmt.Errorf("retained Task %s validation failed: %w", id, err)
		}
		if current.Priority == model.TaskPriorityP0 || current.Priority == model.TaskPriorityP1 || current.Priority == model.TaskPriorityP2 || current.Priority == model.TaskPriorityP3 || current.Priority == model.TaskPriorityP4 {
			continue
		}
		classification, ok := explicitTaskPriorityAuditClassifications[strings.ToLower(current.Priority)]
		if !ok {
			return migration, fmt.Errorf("active Task %s priority %q has no explicit re-audit classification", id, current.Priority)
		}
		oldRecordedTime := current.UpdatedAt
		if current.Revision == 1 {
			oldRecordedTime = current.CreatedAt
		}
		oldRecordedAt := oldRecordedTime.UTC().Format(time.RFC3339Nano)
		oldHistory, err := db.Query(ctx, `SELECT project_id,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=? AND revision=?`, id, storeRevision)
		if err != nil {
			return migration, fmt.Errorf("read retained Task %s history: %w", id, err)
		}
		if len(oldHistory.Rows) > 1 {
			return migration, fmt.Errorf("retained Task %s has duplicate current history", id)
		}
		statements := make([]upstream.Statement, 0, 4)
		if len(oldHistory.Rows) == 0 {
			statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"task", id, current.ProjectID, storeRevision, "migration", "system:task-priority-reaudit", "preserve pre-priority Task payload", []byte(`["legacy"]`), payload, oldRecordedAt}, RequireRowsAffected: 1})
		} else {
			if len(oldHistory.Rows[0]) != 7 || oldHistory.Rows[0][0] != current.ProjectID {
				return migration, fmt.Errorf("retained Task %s current history is not an exact preserved payload", id)
			}
			historyPayload, payloadOK := oldHistory.Rows[0][5].([]byte)
			if !payloadOK || !bytes.Equal(historyPayload, payload) {
				return migration, fmt.Errorf("retained Task %s current history is not an exact preserved payload", id)
			}
		}
		current.Priority = classification.Priority
		current.Revision++
		current.UpdatedAt = migrationTime
		current.RevisionSHA256 = ""
		current.RevisionSHA256, err = model.HashTaskAuthoring(current)
		if err != nil {
			return migration, fmt.Errorf("hash retained Task %s: %w", id, err)
		}
		if current.ReadySeal != nil {
			current.ReadySeal.Revision = current.Revision
			current.ReadySeal.RevisionSHA256 = current.RevisionSHA256
		}
		if err := model.ValidateTaskAuthoring(current); err != nil {
			return migration, fmt.Errorf("migrated Task %s validation failed: %w", id, err)
		}
		newPayload, err := json.Marshal(current)
		if err != nil {
			return migration, err
		}
		recordedAt := current.UpdatedAt.Format(time.RFC3339Nano)
		opID := "task-priority-reaudit-" + id + "-r" + fmt.Sprint(current.Revision)
		changedFields := []byte(`["priority"]`)
		statements = append(statements,
			upstream.Statement{SQL: `UPDATE shared_tasks SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{current.Revision, newPayload, recordedAt, id, storeRevision}, RequireRowsAffected: 1},
			upstream.Statement{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"task", id, current.ProjectID, current.Revision, "migration", "system:task-priority-reaudit", classification.Rationale, changedFields, newPayload, recordedAt}, RequireRowsAffected: 1},
			upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{opID, "task", id, current.Revision, "task-priority-reaudit", newPayload, recordedAt}, RequireRowsAffected: 1},
		)
		migration.Statements = append(migration.Statements, statements...)
	}
	if len(migration.Statements) == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
	}
	return migration, nil
}
