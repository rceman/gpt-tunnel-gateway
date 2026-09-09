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

const sharedTaskSummaryMigrationMaxRows = 256

var explicitTaskSummaries = map[string]string{
	"GTW-TSK433": "Migrate Train planning and Journal onto the SharedLifecycle core after Task execution cleanup, while leaving Rule and Milestone to their dedicated Tasks.",
	"GTW-TSK434": "Prove the complete canonical GPT Tunnel lifecycle on a clean disposable project before BotDuel onboarding, including Task execution, review/rework, integration, release and deployment.",
	"GTW-TSK521": "Implement ADR108 Task execution, review and integration on SharedLifecycle using one reused Agent, immutable review artifacts and verified-tree squash integration.",
	"GTW-TSK527": "Separate Milestone layout authority in ADR95 from reusable status-symbol authority in ADR107 while preserving both ADR identities and history.",
	"GTW-TSK529": "Implement Project release as a durable operation over exact canonical-main source, separate from Task integration and deployment.",
	"GTW-TSK530": "Implement Project deployment as a durable operation for an exact eligible release, separate from Task integration, release creation and break-glass activation.",
	"GTW-TSK550": "Allow requires_new_adr Tasks without existing ADR references while keeping existing-ADR relations fail-closed.",
}

func sharedTaskSummaryMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	migration := migrate.Migration{Version: sharedTaskSummaryMigrationVersion, Name: sharedTaskSummaryMigrationName}
	rows, err := db.Query(ctx, "SELECT id,revision,payload FROM shared_tasks ORDER BY id")
	if err != nil {
		return migration, fmt.Errorf("count Shared Tasks for summary migration: %w", err)
	}
	if len(rows.Rows) == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
		return migration, nil
	}
	if len(rows.Rows) > sharedTaskSummaryMigrationMaxRows {
		return migration, fmt.Errorf("current Task inventory exceeds bounded migration maximum %d", sharedTaskSummaryMigrationMaxRows)
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
		if err := model.ValidateTaskAuthoringRevision(current, true); err != nil {
			return migration, fmt.Errorf("retained Task %s validation failed: %w", id, err)
		}
		if len([]rune(current.Title)) > 128 {
			return migration, fmt.Errorf("retained Task %s title exceeds 128 characters", id)
		}
		if current.Summary != "" {
			continue
		}
		summary, ok := explicitTaskSummaries[id]
		if !ok {
			summary, err = boundedTaskSummary(current.Objective)
		}
		if err != nil {
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
			statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"task", id, current.ProjectID, storeRevision, "migration", "system:task-summary-migration", "preserve pre-summary Task payload", []byte(`["legacy"]`), payload, oldRecordedAt}, RequireRowsAffected: 1})
		} else {
			if len(oldHistory.Rows[0]) != 7 || oldHistory.Rows[0][0] != current.ProjectID {
				return migration, fmt.Errorf("retained Task %s current history is not an exact preserved payload", id)
			}
			historyPayload, payloadOK := oldHistory.Rows[0][5].([]byte)
			if !payloadOK || !bytes.Equal(historyPayload, payload) {
				return migration, fmt.Errorf("retained Task %s current history is not an exact preserved payload", id)
			}
		}
		current.Revision++
		current.UpdatedAt = migrationTime
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

func boundedTaskSummary(objective string) (string, error) {
	words := strings.Fields(objective)
	for i, word := range words {
		if strings.HasSuffix(word, ".") || strings.HasSuffix(word, "!") || strings.HasSuffix(word, "?") {
			candidate := strings.Join(words[:i+1], " ")
			if candidate != "" && len([]rune(candidate)) <= 256 {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("objective has no complete sentence within 256 characters")
}
