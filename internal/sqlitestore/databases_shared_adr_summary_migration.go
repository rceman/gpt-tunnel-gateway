package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const sharedADRSummaryMigrationMaxRows = 256

func sharedADRSummaryMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	migration := migrate.Migration{Version: sharedADRSummaryMigrationVersion, Name: sharedADRSummaryMigrationName}
	rows, err := db.Query(ctx, "SELECT id,revision,payload FROM shared_adrs ORDER BY id LIMIT ?", int64(sharedADRSummaryMigrationMaxRows+1))
	if err != nil {
		return migration, fmt.Errorf("count Shared ADRs for summary migration: %w", err)
	}
	if len(rows.Rows) == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
		return migration, nil
	}
	if len(rows.Rows) > sharedADRSummaryMigrationMaxRows {
		return migration, fmt.Errorf("current ADR inventory exceeds bounded migration maximum %d", sharedADRSummaryMigrationMaxRows)
	}
	migrationTime := time.Now().UTC()
	for _, row := range rows.Rows {
		if len(row) != 3 {
			return migration, fmt.Errorf("invalid current ADR row shape")
		}
		id, idOK := row[0].(string)
		storeRevision, revisionOK := row[1].(int64)
		payload, payloadOK := row[2].([]byte)
		if !idOK || id == "" || !revisionOK || storeRevision < 1 || !payloadOK {
			return migration, fmt.Errorf("retained ADR %s has invalid store row", id)
		}
		var current model.ADR
		if err := json.Unmarshal(payload, &current); err != nil {
			return migration, fmt.Errorf("decode retained ADR %s: %w", id, err)
		}
		if current.ID != id || current.Revision != int(storeRevision) {
			return migration, fmt.Errorf("current ADR %s identity/revision mismatch", id)
		}
		if len([]rune(current.Title)) > model.ADRTitleMaxRunes {
			return migration, fmt.Errorf("retained ADR %s title exceeds %d characters", id, model.ADRTitleMaxRunes)
		}
		if err := model.ValidateADRRevision(current, true); err != nil {
			return migration, fmt.Errorf("retained ADR %s validation failed: %w", id, err)
		}
		if current.Summary != "" {
			continue
		}
		current.Summary = current.Title
		oldRecordedTime := current.CreatedAt
		if current.Revision >= 2 {
			oldRecordedTime = current.UpdatedAt
		}
		oldRecordedAt := oldRecordedTime.UTC().Format(time.RFC3339Nano)
		oldHistory, err := db.Query(ctx, `SELECT project_id,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM shared_entity_revisions WHERE entity_type='adr' AND entity_id=? AND revision=?`, id, storeRevision)
		if err != nil {
			return migration, fmt.Errorf("read retained ADR %s history: %w", id, err)
		}
		if len(oldHistory.Rows) > 1 {
			return migration, fmt.Errorf("retained ADR %s has duplicate current history", id)
		}
		statements := make([]upstream.Statement, 0, 4)
		if len(oldHistory.Rows) == 0 {
			statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"adr", id, current.ProjectID, storeRevision, "migration", "system:adr-summary-migration", "preserve pre-summary ADR payload", []byte(`["legacy"]`), payload, oldRecordedAt}, RequireRowsAffected: 1})
		} else {
			if len(oldHistory.Rows[0]) != 7 || oldHistory.Rows[0][0] != current.ProjectID {
				return migration, fmt.Errorf("retained ADR %s current history is not an exact preserved payload", id)
			}
			historyPayload, payloadOK := oldHistory.Rows[0][5].([]byte)
			if !payloadOK || !bytes.Equal(historyPayload, payload) {
				return migration, fmt.Errorf("retained ADR %s current history is not an exact preserved payload", id)
			}
		}
		current.Revision++
		current.RevisionCount = current.Revision
		current.UpdatedBy = "system:adr-summary-migration"
		current.LastReason = "add ADR summary"
		current.UpdatedAt = migrationTime
		if err := model.ValidateADR(current); err != nil {
			return migration, fmt.Errorf("migrated ADR %s validation failed: %w", id, err)
		}
		newPayload, err := json.Marshal(current)
		if err != nil {
			return migration, err
		}
		recordedAt := current.UpdatedAt.Format(time.RFC3339Nano)
		opID := "adr-summary-migration-" + id + "-r" + fmt.Sprint(current.Revision)
		statements = append(statements,
			upstream.Statement{SQL: `UPDATE shared_adrs SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{current.Revision, newPayload, recordedAt, id, storeRevision}, RequireRowsAffected: 1},
			upstream.Statement{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"adr", id, current.ProjectID, current.Revision, "migration", "system:adr-summary-migration", "add ADR summary", []byte(`["summary"]`), newPayload, recordedAt}, RequireRowsAffected: 1},
			upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{opID, "adr", id, current.Revision, "adr-summary-migration", newPayload, recordedAt}, RequireRowsAffected: 1},
		)
		migration.Statements = append(migration.Statements, statements...)
	}
	if len(migration.Statements) == 0 {
		migration.Statements = []upstream.Statement{{SQL: "SELECT 1"}}
	}
	return migration, nil
}
