package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
)

const (
	SharedLifecycleEventKindStatus  = "status"
	SharedLifecycleEventKindArchive = "archive"
)

func sharedLifecycleEventMigration() migrate.Migration {
	return migrate.Migration{
		Version: sharedLifecycleEventMigrationVersion,
		Name:    sharedLifecycleEventMigrationName,
		Statements: []upstream.Statement{
			{SQL: `CREATE TABLE IF NOT EXISTS shared_lifecycle_events (
id INTEGER PRIMARY KEY AUTOINCREMENT,
operation_id TEXT NOT NULL UNIQUE,
entity_type TEXT NOT NULL,
project_id TEXT NOT NULL,
entity_id TEXT NOT NULL,
revision INTEGER NOT NULL,
event_kind TEXT NOT NULL,
from_status TEXT NOT NULL,
to_status TEXT NOT NULL,
actor TEXT NOT NULL,
reason TEXT NOT NULL,
contract BLOB NOT NULL,
recorded_at TEXT NOT NULL
)`},
			{SQL: `CREATE INDEX IF NOT EXISTS shared_lifecycle_events_entity_idx ON shared_lifecycle_events(entity_type,project_id,entity_id,id)`},
		},
	}
}

type SharedLifecycleEvent struct {
	ID            int64
	OperationID   string
	EntityType    string
	ProjectID     string
	EntityID      string
	Revision      int64
	EventKind     string
	MutationKind  string
	FromStatus    string
	ToStatus      string
	Actor         string
	Reason        string
	Contract      []byte
	ChangedFields []string
	RecordedAt    time.Time
}

const sharedLifecycleEventColumns = `SELECT id,operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields FROM shared_lifecycle_events`

func decodeSharedLifecycleEvent(row []any) (SharedLifecycleEvent, error) {
	var event SharedLifecycleEvent
	if len(row) != 15 {
		return event, fmt.Errorf("invalid shared lifecycle event row")
	}
	text := func(i int) (string, error) {
		value, ok := row[i].(string)
		if !ok {
			return "", fmt.Errorf("invalid shared lifecycle event column %d", i)
		}
		return value, nil
	}
	id, ok := row[0].(int64)
	if !ok {
		return event, fmt.Errorf("invalid shared lifecycle event id")
	}
	revision, ok := row[5].(int64)
	if !ok {
		return event, fmt.Errorf("invalid shared lifecycle event revision")
	}
	contract, ok := row[11].([]byte)
	if !ok {
		return event, fmt.Errorf("invalid shared lifecycle event contract")
	}
	recorded, err := text(12)
	if err != nil {
		return event, err
	}
	at, err := time.Parse(time.RFC3339Nano, recorded)
	if err != nil {
		return event, fmt.Errorf("invalid shared lifecycle event recorded_at")
	}
	event.ID = id
	event.Revision = revision
	event.Contract = append([]byte(nil), contract...)
	event.RecordedAt = at
	if event.OperationID, err = text(1); err != nil {
		return event, err
	}
	if event.EntityType, err = text(2); err != nil {
		return event, err
	}
	if event.ProjectID, err = text(3); err != nil {
		return event, err
	}
	if event.EntityID, err = text(4); err != nil {
		return event, err
	}
	if event.EventKind, err = text(6); err != nil {
		return event, err
	}
	if event.FromStatus, err = text(7); err != nil {
		return event, err
	}
	if event.ToStatus, err = text(8); err != nil {
		return event, err
	}
	if event.Actor, err = text(9); err != nil {
		return event, err
	}
	if event.Reason, err = text(10); err != nil {
		return event, err
	}
	if event.MutationKind, err = text(13); err != nil {
		return event, err
	}
	changedFields, ok := row[14].([]byte)
	if !ok {
		return event, fmt.Errorf("invalid shared lifecycle event changed fields")
	}
	if event.ChangedFields, err = decodeSharedLifecycleChangedFields(changedFields); err != nil {
		return event, err
	}
	definition, ok := sharedLifecycle(event.EntityType)
	if event.ID < 1 || event.Revision < 1 || !ok || definition.DefaultCreateStatus == "" ||
		event.OperationID == "" || event.ProjectID == "" || event.EntityID == "" ||
		(event.EventKind != SharedLifecycleEventKindStatus && event.EventKind != SharedLifecycleEventKindArchive) ||
		event.FromStatus == event.ToStatus ||
		!containsString(definition.AllowedStatuses, event.FromStatus) || !containsString(definition.AllowedStatuses, event.ToStatus) ||
		!containsString(definition.AllowedTransitions[event.FromStatus], event.ToStatus) ||
		validateSharedLifecycleEventKind(definition, event.EventKind, event.ToStatus) != nil ||
		event.MutationKind != sharedLifecycleMutationKind(definition, event.EventKind) ||
		event.Actor == "" || strings.ContainsRune(event.Actor, 0) || len([]rune(event.Actor)) > 256 ||
		!validSharedHistoryReason(event.Reason) ||
		len(event.Contract) == 0 {
		return event, fmt.Errorf("invalid shared lifecycle event identity")
	}
	return event, nil
}

func decodeSharedLifecycleChangedFields(raw []byte) ([]string, error) {
	var fields []string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return nil, fmt.Errorf("invalid shared lifecycle event changed fields")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid shared lifecycle event changed fields")
	}
	if err := validateSharedLifecycleChangedFields(fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func validateSharedLifecycleChangedFields(fields []string) error {
	if len(fields) < 1 || len(fields) > 16 {
		return fmt.Errorf("invalid shared lifecycle event changed fields")
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if field == "" || len([]rune(field)) > 64 || strings.ContainsRune(field, 0) || seen[field] {
			return fmt.Errorf("invalid shared lifecycle event changed fields")
		}
		seen[field] = true
	}
	return nil
}

func (d *Databases) EnsureSharedLifecycleEvent(ctx context.Context, event SharedLifecycleEvent) error {
	changedFields, err := json.Marshal(event.ChangedFields)
	if err != nil {
		return err
	}
	recordedAt := event.RecordedAt.UTC().Format(time.RFC3339Nano)
	validated, err := decodeSharedLifecycleEvent([]any{
		int64(1), event.OperationID, event.EntityType, event.ProjectID, event.EntityID, event.Revision,
		event.EventKind, event.FromStatus, event.ToStatus, event.Actor, event.Reason, event.Contract,
		recordedAt, event.MutationKind, changedFields,
	})
	if err != nil {
		return err
	}
	current, err := d.ReadSharedEntity(ctx, event.EntityType, event.EntityID)
	if err != nil {
		return err
	}
	if current.Revision < event.Revision {
		return fmt.Errorf("Shared %s lifecycle event exceeds current revision", event.EntityType)
	}
	read := func() (SharedLifecycleEvent, bool, error) {
		rows, err := d.Shared.Query(ctx, sharedLifecycleEventColumns+` WHERE operation_id=?`, event.OperationID)
		if err != nil {
			return SharedLifecycleEvent{}, false, err
		}
		if len(rows.Rows) == 0 {
			return SharedLifecycleEvent{}, false, nil
		}
		if len(rows.Rows) != 1 {
			return SharedLifecycleEvent{}, false, fmt.Errorf("duplicate Shared lifecycle operation identity")
		}
		stored, err := decodeSharedLifecycleEvent(rows.Rows[0])
		return stored, err == nil, err
	}
	if stored, found, err := read(); err != nil {
		return err
	} else if found {
		if sameSharedLifecycleEvent(stored, validated) {
			return nil
		}
		return fmt.Errorf("conflicting Shared lifecycle operation %q", event.OperationID)
	}
	_, err = d.Shared.Exec(ctx, `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, validated.OperationID, validated.EntityType, validated.ProjectID, validated.EntityID, validated.Revision, validated.EventKind, validated.FromStatus, validated.ToStatus, validated.Actor, validated.Reason, validated.Contract, recordedAt, validated.MutationKind, changedFields)
	if err != nil {
		if stored, found, readErr := read(); readErr == nil && found && sameSharedLifecycleEvent(stored, validated) {
			return nil
		}
		return err
	}
	return nil
}

func sameSharedLifecycleEvent(left, right SharedLifecycleEvent) bool {
	return left.OperationID == right.OperationID && left.EntityType == right.EntityType && left.ProjectID == right.ProjectID && left.EntityID == right.EntityID && left.Revision == right.Revision && left.EventKind == right.EventKind && left.MutationKind == right.MutationKind && left.FromStatus == right.FromStatus && left.ToStatus == right.ToStatus && left.Actor == right.Actor && left.Reason == right.Reason && bytes.Equal(left.Contract, right.Contract) && reflect.DeepEqual(left.ChangedFields, right.ChangedFields) && left.RecordedAt.Equal(right.RecordedAt)
}

func (d *Databases) ListSharedLifecycleEvents(ctx context.Context, entityType, projectID, entityID string, limit int) ([]SharedLifecycleEvent, error) {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.DefaultCreateStatus == "" || d == nil || d.Shared == nil {
		return nil, fmt.Errorf("shared lifecycle %q has no lifecycle events", entityType)
	}
	if projectID == "" || entityID == "" || limit < 1 || limit > SharedLifecycleQueryMaxRows {
		return nil, fmt.Errorf("invalid shared %s lifecycle event limit", entityType)
	}
	events, err := d.readSharedLifecycleChain(ctx, definition, projectID, entityID, limit)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (d *Databases) readSharedLifecycleChain(ctx context.Context, definition sharedLifecycleDefinition, projectID, entityID string, limit int) ([]SharedLifecycleEvent, error) {
	rows, err := d.Shared.Query(ctx, sharedLifecycleEventColumns+` WHERE entity_type=? AND project_id=? AND entity_id=? ORDER BY id ASC LIMIT ?`, definition.EntityType, projectID, entityID, int64(limit)+1)
	if err != nil {
		return nil, err
	}
	if len(rows.Rows) > limit {
		return nil, fmt.Errorf("shared %s lifecycle chain exceeds bounded maximum %d", definition.EntityType, limit)
	}
	events := make([]SharedLifecycleEvent, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		event, err := decodeSharedLifecycleEvent(row)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := d.validateSharedLifecycleChain(ctx, definition, projectID, entityID, events); err != nil {
		return nil, err
	}
	return events, nil
}

func (d *Databases) validateSharedLifecycleChain(ctx context.Context, definition sharedLifecycleDefinition, projectID, entityID string, events []SharedLifecycleEvent) error {
	for index := 1; index < len(events); index++ {
		if events[index].FromStatus != events[index-1].ToStatus {
			return fmt.Errorf("shared %s lifecycle chain is discontinuous at event %d", definition.EntityType, events[index].ID)
		}
	}
	if len(events) == 0 {
		return nil
	}
	rows, err := d.Shared.Query(ctx, fmt.Sprintf("SELECT payload FROM %s WHERE id=?", definition.StateTable), entityID)
	if err != nil {
		return err
	}
	if len(rows.Rows) != 1 {
		return fmt.Errorf("shared %s lifecycle chain has no current state", definition.EntityType)
	}
	payload, ok := rows.Rows[0][0].([]byte)
	if !ok {
		return fmt.Errorf("invalid shared %s lifecycle state payload", definition.EntityType)
	}
	var current struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(payload, &current); err != nil {
		return fmt.Errorf("invalid shared %s lifecycle state payload", definition.EntityType)
	}
	if current.Status != events[len(events)-1].ToStatus {
		return fmt.Errorf("shared %s lifecycle chain does not agree with the current state status", definition.EntityType)
	}
	return nil
}

type SharedLifecycleEventRequest struct {
	OperationID           string
	EntityType            string
	ProjectID             string
	EntityID              string
	ExpectedRevision      int64
	ExpectedStoreRevision int64
	ExpectedPayload       []byte
	Revision              int64
	Kind                  string
	EventKind             string
	HistoryMutationKind   string
	FromStatus            string
	ToStatus              string
	Payload               []byte
	Actor                 string
	Reason                string
	ChangedFields         []string
	Contract              []byte
	CreatedAt             time.Time
	PreviousHistory       *SharedHistorySeed
	ExtraStatements       []upstream.Statement
}

func (d *Databases) CommitSharedLifecycleEvent(ctx context.Context, request SharedLifecycleEventRequest) (SharedMutationReceipt, error) {
	definition, ok := sharedLifecycle(request.EntityType)
	if !ok || definition.HistoryTable == "" || definition.DefaultCreateStatus == "" {
		return SharedMutationReceipt{}, fmt.Errorf("shared lifecycle %q has no lifecycle events", request.EntityType)
	}
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, fmt.Errorf("shared store is unavailable")
	}
	if request.OperationID == "" || request.ProjectID == "" || request.EntityID == "" || request.Kind == "" || request.EventKind == "" ||
		request.ExpectedRevision < 1 || request.ExpectedStoreRevision < 1 || len(request.ExpectedPayload) == 0 || len(request.Payload) == 0 ||
		request.Actor == "" || !validSharedHistoryReason(request.Reason) || len(request.Contract) == 0 {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s lifecycle event", request.EntityType)
	}
	contentChanged := request.Revision == request.ExpectedRevision+1
	if !contentChanged && request.Revision != request.ExpectedRevision {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s lifecycle event revision", request.EntityType)
	}
	payloadRevision, err := sharedPayloadLogicalRevision(request.Payload)
	if err != nil || payloadRevision != request.Revision {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s logical revision", request.EntityType)
	}
	if err := validateSharedLifecycleStatus(definition, request.ExpectedPayload, request.Payload, false); err != nil {
		return SharedMutationReceipt{}, err
	}
	var previous, next struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(request.ExpectedPayload, &previous); err != nil {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s previous payload", request.EntityType)
	}
	if err := json.Unmarshal(request.Payload, &next); err != nil {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s payload", request.EntityType)
	}
	if previous.Status == next.Status || previous.Status != request.FromStatus || next.Status != request.ToStatus {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s lifecycle transition", request.EntityType)
	}
	if err := validateSharedLifecycleEventKind(definition, request.EventKind, request.ToStatus); err != nil {
		return SharedMutationReceipt{}, err
	}
	if request.HistoryMutationKind != sharedLifecycleMutationKind(definition, request.EventKind) {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s lifecycle mutation kind", request.EntityType)
	}
	if err := validateSharedLifecycleChangedFields(request.ChangedFields); err != nil {
		return SharedMutationReceipt{}, err
	}
	if request.PreviousHistory != nil && request.PreviousHistory.Revision != request.ExpectedRevision {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s history seed", request.EntityType)
	}
	if len(request.ExtraStatements) > 4 {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s lifecycle side effects", request.EntityType)
	}
	for _, statement := range request.ExtraStatements {
		if strings.TrimSpace(statement.SQL) == "" || statement.RequireRowsAffected < 1 {
			return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s lifecycle side effect", request.EntityType)
		}
	}
	recorded := request.CreatedAt.UTC().Format(time.RFC3339Nano)
	if request.CreatedAt.IsZero() {
		recorded = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if existing, found, err := d.outboxEntry(ctx, request.OperationID); err != nil {
		return SharedMutationReceipt{}, err
	} else if found {
		return d.reuseSharedMutation(SharedMutation{
			OperationID: request.OperationID,
			EntityType:  request.EntityType,
			EntityID:    existing.EntityID,
			Revision:    existing.Revision,
			Kind:        request.Kind,
			Payload:     existing.Payload,
		}, existing)
	}
	changedFields, err := json.Marshal(request.ChangedFields)
	if err != nil {
		return SharedMutationReceipt{}, err
	}
	statements := make([]upstream.Statement, 0, 5)
	if seed := request.PreviousHistory; seed != nil {
		if seed.MutationKind == "" || seed.Actor == "" || !validSharedHistoryReason(seed.Reason) || len(seed.Payload) == 0 || seed.RecordedAt == "" {
			return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s history seed", request.EntityType)
		}
		seedFields, marshalErr := json.Marshal(seed.ChangedFields)
		if marshalErr != nil {
			return SharedMutationReceipt{}, marshalErr
		}
		statements = append(statements, sharedHistorySeedStatement(definition, request.EntityID, request.ProjectID, seed, seedFields))
	}
	stateCAS := fmt.Sprintf("id=? AND revision=? AND payload=? AND COALESCE(CAST(json_extract(payload, '$.revision') AS INTEGER), 1)=?")
	if contentChanged {
		statements = append(statements,
			upstream.Statement{SQL: fmt.Sprintf("UPDATE %s SET revision=?,payload=?,updated_at=? WHERE %s", definition.StateTable, stateCAS), Args: []any{request.Revision, request.Payload, recorded, request.EntityID, request.ExpectedStoreRevision, request.ExpectedPayload, request.ExpectedRevision}, RequireRowsAffected: 1},
			sharedHistoryInsertStatement(definition, request.EntityID, request.ProjectID, request.Revision, "update", request.Actor, request.Reason, changedFields, request.Payload, recorded),
		)
	} else {
		statements = append(statements,
			upstream.Statement{SQL: fmt.Sprintf("UPDATE %s SET payload=?,updated_at=? WHERE %s", definition.StateTable, stateCAS), Args: []any{request.Payload, recorded, request.EntityID, request.ExpectedStoreRevision, request.ExpectedPayload, request.ExpectedRevision}, RequireRowsAffected: 1},
		)
	}
	statements = append(statements, request.ExtraStatements...)
	statements = append(statements,
		upstream.Statement{SQL: `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Args: []any{request.OperationID, request.EntityType, request.ProjectID, request.EntityID, request.Revision, request.EventKind, request.FromStatus, request.ToStatus, request.Actor, request.Reason, request.Contract, recorded, request.HistoryMutationKind, changedFields}, RequireRowsAffected: 1},
		upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{request.OperationID, request.EntityType, request.EntityID, request.Revision, request.Kind, request.Payload, recorded}, RequireRowsAffected: 1},
	)
	if _, err := d.Shared.Batch(ctx, statements); err != nil {
		if existing, found, readErr := d.outboxEntry(ctx, request.OperationID); readErr == nil && found {
			return d.reuseSharedMutation(SharedMutation{
				OperationID: request.OperationID,
				EntityType:  request.EntityType,
				EntityID:    existing.EntityID,
				Revision:    existing.Revision,
				Kind:        request.Kind,
				Payload:     existing.Payload,
			}, existing)
		}
		return SharedMutationReceipt{}, err
	}
	return SharedMutationReceipt{
		OperationID: request.OperationID,
		EntityType:  request.EntityType,
		EntityID:    request.EntityID,
		Revision:    request.Revision,
		Committed:   true,
	}, nil
}

type SharedLifecycleHistoryCursor struct {
	RecordedAt string `json:"recorded_at"`
	Revision   int64  `json:"revision"`
	Source     int    `json:"source"`
	ID         int64  `json:"id"`
}

func EncodeSharedLifecycleHistoryCursor(cursor SharedLifecycleHistoryCursor) (string, error) {
	if err := validateSharedLifecycleHistoryCursor(cursor, false); err != nil {
		return "", err
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func DecodeSharedLifecycleHistoryCursor(raw string) (SharedLifecycleHistoryCursor, error) {
	var cursor SharedLifecycleHistoryCursor
	if raw == "" {
		return SharedLifecycleHistoryCursor{}, fmt.Errorf("invalid shared history cursor")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return SharedLifecycleHistoryCursor{}, fmt.Errorf("invalid shared history cursor")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return SharedLifecycleHistoryCursor{}, fmt.Errorf("invalid shared history cursor")
	}
	if err := validateSharedLifecycleHistoryCursor(cursor, false); err != nil {
		return SharedLifecycleHistoryCursor{}, err
	}
	return cursor, nil
}

func validateSharedLifecycleHistoryCursor(cursor SharedLifecycleHistoryCursor, allowZero bool) error {
	if allowZero && cursor == (SharedLifecycleHistoryCursor{}) {
		return nil
	}
	if cursor.Source != 0 && cursor.Source != 1 {
		return fmt.Errorf("invalid shared history cursor source")
	}
	if cursor.Revision < 0 {
		return fmt.Errorf("invalid shared history cursor revision")
	}
	if cursor.ID < 1 {
		return fmt.Errorf("invalid shared history cursor id")
	}
	parsed, err := time.Parse(time.RFC3339Nano, cursor.RecordedAt)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != cursor.RecordedAt {
		return fmt.Errorf("invalid shared history cursor timestamp")
	}
	return nil
}

func (d *Databases) ListSharedLifecycleHistoryPage(ctx context.Context, entityType, projectID, entityID string, after SharedLifecycleHistoryCursor, limit int) (SharedHistoryPage, error) {
	definition, ok := sharedLifecycle(entityType)
	if !ok || definition.HistoryTable == "" || d == nil || d.Shared == nil {
		return SharedHistoryPage{}, fmt.Errorf("shared lifecycle %q has no revision history", entityType)
	}
	if projectID == "" || entityID == "" || limit < 1 || limit > SharedLifecycleQueryMaxRows {
		return SharedHistoryPage{}, fmt.Errorf("invalid %s history page", entityType)
	}
	if err := validateSharedLifecycleHistoryCursor(after, true); err != nil {
		return SharedHistoryPage{}, err
	}
	if _, err := d.readSharedLifecycleChain(ctx, definition, projectID, entityID, SharedLifecycleQueryMaxRows); err != nil {
		return SharedHistoryPage{}, err
	}
	type keyed struct {
		record   SharedRevisionRecord
		revision int64
		source   int
		id       int64
		at       time.Time
	}
	var merged []keyed
	contentSQL := fmt.Sprintf("SELECT %s,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at FROM %s WHERE %s=? AND project_id=? AND %s=? AND revision>? ORDER BY revision ASC LIMIT ?", definition.HistoryIDColumn, definition.HistoryTable, definition.HistoryEntityColumn, definition.HistoryIDColumn)
	contentRows, err := d.Shared.Query(ctx, contentSQL, entityType, projectID, entityID, after.Revision, int64(limit)+1)
	if err != nil {
		return SharedHistoryPage{}, err
	}
	for _, row := range contentRows.Rows {
		record, err := decodeSharedRevisionRow(row)
		if err != nil {
			return SharedHistoryPage{}, err
		}
		at, err := time.Parse(time.RFC3339Nano, record.RecordedAt)
		if err != nil {
			return SharedHistoryPage{}, fmt.Errorf("invalid shared revision recorded_at")
		}
		merged = append(merged, keyed{
			record:   record,
			revision: record.Revision,
			source:   0,
			id:       record.Revision,
			at:       at,
		})
	}
	var eventSQL string
	var eventArgs []any
	if after == (SharedLifecycleHistoryCursor{}) || after.Source == 0 {
		eventSQL = sharedLifecycleEventColumns + ` WHERE entity_type=? AND project_id=? AND entity_id=? AND revision>=? ORDER BY revision ASC, id ASC LIMIT ?`
		eventArgs = []any{entityType, projectID, entityID, after.Revision, int64(limit) + 1}
	} else {
		eventSQL = sharedLifecycleEventColumns + ` WHERE entity_type=? AND project_id=? AND entity_id=? AND (revision>? OR (revision=? AND id>?)) ORDER BY revision ASC, id ASC LIMIT ?`
		eventArgs = []any{entityType, projectID, entityID, after.Revision, after.Revision, after.ID, int64(limit) + 1}
	}
	eventRows, err := d.Shared.Query(ctx, eventSQL, eventArgs...)
	if err != nil {
		return SharedHistoryPage{}, err
	}
	for _, row := range eventRows.Rows {
		event, err := decodeSharedLifecycleEvent(row)
		if err != nil {
			return SharedHistoryPage{}, err
		}
		if event.EntityType != entityType || event.ProjectID != projectID || event.EntityID != entityID {
			return SharedHistoryPage{}, fmt.Errorf("shared lifecycle event ownership mismatch")
		}
		merged = append(merged, keyed{
			record: SharedRevisionRecord{
				EntityID:      event.EntityID,
				ProjectID:     event.ProjectID,
				Revision:      event.Revision,
				MutationKind:  event.MutationKind,
				Actor:         event.Actor,
				Reason:        event.Reason,
				ChangedFields: append([]string(nil), event.ChangedFields...),
				Payload:       append([]byte(nil), event.Contract...),
				RecordedAt:    event.RecordedAt.UTC().Format(time.RFC3339Nano),
			},
			revision: event.Revision,
			source:   1,
			id:       event.ID,
			at:       event.RecordedAt.UTC(),
		})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].revision != merged[j].revision {
			return merged[i].revision < merged[j].revision
		}
		if merged[i].source != merged[j].source {
			return merged[i].source < merged[j].source
		}
		return merged[i].id < merged[j].id
	})
	page := SharedHistoryPage{Records: make([]SharedRevisionRecord, 0, len(merged))}
	if len(merged) > limit {
		page.HasMore = true
		merged = merged[:limit]
	}
	for _, item := range merged {
		page.Records = append(page.Records, item.record)
	}
	if page.HasMore {
		last := merged[len(merged)-1]
		cursor, err := EncodeSharedLifecycleHistoryCursor(SharedLifecycleHistoryCursor{
			RecordedAt: last.at.UTC().Format(time.RFC3339Nano),
			Revision:   last.revision,
			Source:     last.source,
			ID:         last.id,
		})
		if err != nil {
			return SharedHistoryPage{}, err
		}
		page.NextCursor = cursor
		page.NextRevision = last.revision
	}
	return page, nil
}
