package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const sharedTaskLifecycleHardCutMaxRows = 256

const sharedTaskLifecycleEventColumns = `SELECT id,operation_id,project_id,task_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at FROM shared_task_lifecycle_events`

const sharedTaskLifecycleChangedFields = `["status"]`

const sharedTaskLifecycleReadyChangedFields = `["status","ready_seal"]`

type retainedTaskLifecycleEvent struct {
	ID          int64
	OperationID string
	ProjectID   string
	TaskID      string
	Revision    int64
	Kind        string
	FromStatus  string
	ToStatus    string
	Actor       string
	Reason      string
	Contract    []byte
	RecordedAt  string
}

func sharedTaskLifecycleHardCutMigrationMarker() migrate.Migration {
	return migrate.Migration{
		Version:    sharedTaskLifecycleHardCutMigrationVersion,
		Name:       sharedTaskLifecycleHardCutMigrationName,
		Statements: []upstream.Statement{{SQL: "SELECT 1"}},
	}
}

func sharedTaskLifecycleHardCutMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	migration := migrate.Migration{Version: sharedTaskLifecycleHardCutMigrationVersion, Name: sharedTaskLifecycleHardCutMigrationName}
	definition, ok := sharedLifecycle("task")
	if !ok || definition.DefaultCreateStatus == "" || definition.StatusMutationKind == "" || definition.ArchiveMutationKind == "" {
		return migration, fmt.Errorf("shared task lifecycle descriptor is unavailable")
	}
	if err := validateRetainedSharedLifecycleEvents(ctx, db, definition); err != nil {
		return migration, err
	}
	rows, err := db.Query(ctx, sharedTaskLifecycleEventColumns+` ORDER BY task_id ASC, revision ASC, id ASC LIMIT ?`, int64(sharedTaskLifecycleHardCutMaxRows+1))
	if err != nil {
		return migration, fmt.Errorf("read retained task lifecycle events: %w", err)
	}
	if len(rows.Rows) > sharedTaskLifecycleHardCutMaxRows {
		return migration, fmt.Errorf("retained task lifecycle event inventory exceeds bounded migration maximum %d", sharedTaskLifecycleHardCutMaxRows)
	}
	events := make([]retainedTaskLifecycleEvent, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		event, err := decodeRetainedTaskLifecycleEvent(row)
		if err != nil {
			return migration, err
		}
		events = append(events, event)
	}
	if err := validateRetainedTaskLifecycleChains(ctx, db, definition, events); err != nil {
		return migration, err
	}
	if err := validateRetainedTaskLifecycleDestination(ctx, db, events); err != nil {
		return migration, err
	}
	migration.Statements = append(migration.Statements,
		upstream.Statement{SQL: `ALTER TABLE shared_lifecycle_events ADD COLUMN mutation_kind TEXT NOT NULL DEFAULT ''`},
		upstream.Statement{SQL: `ALTER TABLE shared_lifecycle_events ADD COLUMN changed_fields BLOB NOT NULL DEFAULT '[]'`},
		upstream.Statement{SQL: `UPDATE shared_lifecycle_events SET mutation_kind='status', changed_fields=CAST('["status"]' AS BLOB) WHERE entity_type='adr' AND event_kind='status'`},
		upstream.Statement{SQL: `UPDATE shared_lifecycle_events SET mutation_kind='archive', changed_fields=CAST('["status","archived_at","archived_by","archive_reason"]' AS BLOB) WHERE entity_type='adr' AND event_kind='archive'`},
	)
	for _, event := range events {
		eventKind := SharedLifecycleEventKindArchive
		mutationKind := definition.ArchiveMutationKind
		changedFields := sharedTaskLifecycleChangedFields
		if event.Kind == TaskLifecycleEventKindComplete {
			eventKind = SharedLifecycleEventKindStatus
			mutationKind = definition.StatusMutationKind
		}
		if event.FromStatus == model.TaskAuthoringReady {
			changedFields = sharedTaskLifecycleReadyChangedFields
		}
		migration.Statements = append(migration.Statements, upstream.Statement{
			SQL:                 `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Args:                []any{event.OperationID, "task", event.ProjectID, event.TaskID, event.Revision, eventKind, event.FromStatus, event.ToStatus, event.Actor, event.Reason, event.Contract, event.RecordedAt, mutationKind, []byte(changedFields)},
			RequireRowsAffected: 1,
		})
	}
	migration.Statements = append(migration.Statements,
		upstream.Statement{SQL: `DROP INDEX IF EXISTS shared_task_lifecycle_events_task_idx`},
		upstream.Statement{SQL: `DROP TABLE IF EXISTS shared_task_lifecycle_events`},
	)
	return migration, nil
}

func validateRetainedSharedLifecycleEvents(ctx context.Context, db *upstream.Store, definition sharedLifecycleDefinition) error {
	extended, err := sharedLifecycleEventsCarryMutationKind(ctx, db)
	if err != nil {
		return err
	}
	if extended {
		return fmt.Errorf("retained shared lifecycle events already carry a mutation kind")
	}
	rows, err := db.Query(ctx, `SELECT entity_type,event_kind FROM shared_lifecycle_events LIMIT ?`, int64(sharedTaskLifecycleHardCutMaxRows+1))
	if err != nil {
		return fmt.Errorf("read retained shared lifecycle events: %w", err)
	}
	if len(rows.Rows) > sharedTaskLifecycleHardCutMaxRows {
		return fmt.Errorf("retained shared lifecycle event inventory exceeds bounded migration maximum %d", sharedTaskLifecycleHardCutMaxRows)
	}
	for _, row := range rows.Rows {
		if len(row) != 2 {
			return fmt.Errorf("invalid retained shared lifecycle event row shape")
		}
		entityType, ok := row[0].(string)
		if !ok || entityType != "adr" {
			return fmt.Errorf("retained shared lifecycle event has an unsupported entity type")
		}
		eventKind, ok := row[1].(string)
		if !ok || (eventKind != SharedLifecycleEventKindStatus && eventKind != SharedLifecycleEventKindArchive) {
			return fmt.Errorf("retained shared lifecycle event has an unsupported event kind")
		}
	}
	return nil
}

func sharedLifecycleEventsCarryMutationKind(ctx context.Context, db *upstream.Store) (bool, error) {
	rows, err := db.Query(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='shared_lifecycle_events'`)
	if err != nil {
		return false, fmt.Errorf("read retained shared lifecycle event schema: %w", err)
	}
	if len(rows.Rows) != 1 {
		return false, fmt.Errorf("retained shared lifecycle event schema is unavailable")
	}
	schema, ok := rows.Rows[0][0].(string)
	if !ok {
		return false, fmt.Errorf("invalid retained shared lifecycle event schema")
	}
	mutationKind := strings.Contains(schema, "mutation_kind")
	changedFields := strings.Contains(schema, "changed_fields")
	if mutationKind != changedFields {
		return false, fmt.Errorf("retained shared lifecycle events carry a partial mutation kind schema")
	}
	return mutationKind, nil
}

func validateRetainedTaskLifecycleDestination(ctx context.Context, db *upstream.Store, events []retainedTaskLifecycleEvent) error {
	for _, event := range events {
		conflict, err := db.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events WHERE operation_id=?`, event.OperationID)
		if err != nil {
			return fmt.Errorf("read retained shared lifecycle authority: %w", err)
		}
		if conflict.Rows[0][0] != int64(0) {
			return fmt.Errorf("retained task lifecycle operation identity conflicts with shared authority")
		}
		authority, err := db.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events WHERE entity_type='task' AND project_id=? AND entity_id=?`, event.ProjectID, event.TaskID)
		if err != nil {
			return fmt.Errorf("read retained shared lifecycle authority: %w", err)
		}
		if authority.Rows[0][0] != int64(0) {
			return fmt.Errorf("retained task %s already has shared lifecycle authority", event.TaskID)
		}
	}
	return nil
}

func validateRetainedTaskLifecycleChains(ctx context.Context, db *upstream.Store, definition sharedLifecycleDefinition, events []retainedTaskLifecycleEvent) error {
	grouped := make(map[string][]retainedTaskLifecycleEvent, len(events))
	order := make([]string, 0, len(events))
	for _, event := range events {
		key := event.ProjectID + "\x00" + event.TaskID
		if _, seen := grouped[key]; !seen {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], event)
	}
	for _, key := range order {
		chain := grouped[key]
		for index := 1; index < len(chain); index++ {
			if chain[index].FromStatus != chain[index-1].ToStatus {
				return fmt.Errorf("retained task %s lifecycle chain is discontinuous at event %d", chain[index].TaskID, chain[index].ID)
			}
		}
		last := chain[len(chain)-1]
		if !containsString(definition.AllowedStatuses, last.ToStatus) {
			return fmt.Errorf("retained task %s lifecycle chain ends outside the shared status vocabulary", last.TaskID)
		}
		rows, err := db.Query(ctx, `SELECT revision,payload FROM shared_tasks WHERE id=?`, last.TaskID)
		if err != nil {
			return fmt.Errorf("read retained task %s state: %w", last.TaskID, err)
		}
		if len(rows.Rows) != 1 {
			return fmt.Errorf("retained task %s lifecycle chain has no current state", last.TaskID)
		}
		storeRevision, ok := rows.Rows[0][0].(int64)
		if !ok || storeRevision < 1 {
			return fmt.Errorf("invalid retained task %s state revision", last.TaskID)
		}
		payload, ok := rows.Rows[0][1].([]byte)
		if !ok {
			return fmt.Errorf("invalid retained task %s state payload", last.TaskID)
		}
		var current struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
			Revision  int    `json:"revision"`
			Status    string `json:"status"`
		}
		if err := json.Unmarshal(payload, &current); err != nil {
			return fmt.Errorf("invalid retained task %s state payload", last.TaskID)
		}
		if current.ID != last.TaskID || current.ProjectID != last.ProjectID || int64(current.Revision) != storeRevision {
			return fmt.Errorf("retained task %s lifecycle chain does not agree with its current identity", last.TaskID)
		}
		if last.Revision > storeRevision {
			return fmt.Errorf("retained task %s lifecycle chain revision exceeds its current revision", last.TaskID)
		}
		if current.Status != last.ToStatus {
			return fmt.Errorf("retained task %s lifecycle chain does not agree with its current status", last.TaskID)
		}
	}
	return nil
}

func decodeRetainedTaskLifecycleEvent(row []any) (retainedTaskLifecycleEvent, error) {
	var event retainedTaskLifecycleEvent
	if len(row) != 12 {
		return event, fmt.Errorf("invalid retained task lifecycle event row shape")
	}
	id, ok := row[0].(int64)
	if !ok || id < 1 {
		return event, fmt.Errorf("invalid retained task lifecycle event id")
	}
	text := func(index int, label string) (string, error) {
		value, ok := row[index].(string)
		if !ok {
			return "", fmt.Errorf("invalid retained task lifecycle event %s", label)
		}
		return value, nil
	}
	event.ID = id
	revision, ok := row[4].(int64)
	if !ok || revision < 1 {
		return event, fmt.Errorf("invalid retained task lifecycle event revision")
	}
	event.Revision = revision
	contract, ok := row[10].([]byte)
	if !ok {
		return event, fmt.Errorf("invalid retained task lifecycle event contract")
	}
	event.Contract = append([]byte(nil), contract...)
	var err error
	if event.OperationID, err = text(1, "operation identity"); err != nil {
		return event, err
	}
	if event.ProjectID, err = text(2, "project identity"); err != nil {
		return event, err
	}
	if event.TaskID, err = text(3, "task identity"); err != nil {
		return event, err
	}
	if event.Kind, err = text(5, "event kind"); err != nil {
		return event, err
	}
	if event.FromStatus, err = text(6, "source status"); err != nil {
		return event, err
	}
	if event.ToStatus, err = text(7, "target status"); err != nil {
		return event, err
	}
	if event.Actor, err = text(8, "actor"); err != nil {
		return event, err
	}
	if event.Reason, err = text(9, "reason"); err != nil {
		return event, err
	}
	if event.RecordedAt, err = text(11, "recorded_at"); err != nil {
		return event, err
	}
	if model.ValidateProjectIdentifier(event.ProjectID) != nil || model.ValidateCanonicalTaskID(event.TaskID) != nil ||
		event.OperationID == "" || len(event.Contract) == 0 {
		return event, fmt.Errorf("invalid retained task lifecycle event identity")
	}
	if event.Kind != TaskLifecycleEventKindComplete && event.Kind != TaskLifecycleEventKindArchive {
		return event, fmt.Errorf("invalid retained task lifecycle event kind")
	}
	if event.Kind == TaskLifecycleEventKindComplete &&
		(event.ToStatus != model.TaskAuthoringDone || event.FromStatus != model.TaskAuthoringPlanned && event.FromStatus != model.TaskAuthoringReady) {
		return event, fmt.Errorf("invalid retained task completion event")
	}
	if event.Kind == TaskLifecycleEventKindArchive && (event.ToStatus != model.TaskAuthoringArchived ||
		event.FromStatus != model.TaskAuthoringPlanned && event.FromStatus != model.TaskAuthoringReady && event.FromStatus != model.TaskAuthoringDone) {
		return event, fmt.Errorf("invalid retained task archive event")
	}
	if strings.ContainsRune(event.Actor, 0) || utf8.RuneCountInString(event.Actor) < 1 || utf8.RuneCountInString(event.Actor) > 256 {
		return event, fmt.Errorf("invalid retained task lifecycle event actor")
	}
	if strings.ContainsRune(event.Reason, 0) || utf8.RuneCountInString(event.Reason) < 1 || utf8.RuneCountInString(event.Reason) > 1024 {
		return event, fmt.Errorf("invalid retained task lifecycle event reason")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, event.RecordedAt); err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != event.RecordedAt {
		return event, fmt.Errorf("invalid retained task lifecycle event recorded_at")
	}
	return event, nil
}
