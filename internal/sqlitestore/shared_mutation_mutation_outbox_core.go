package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

// SharedMutation is the local-first commit unit for one syncable entity. The
// entity CAS and its Hub outbox row are committed by one SQLite batch before a
// caller acknowledges the mutation. Hub synchronization is deliberately not
// part of this operation.
type SharedMutation struct {
	OperationID       string
	EntityType        string
	EntityID          string
	ExpectedRevision  int64
	Revision          int64
	Kind              string
	Payload           []byte
	CreatedAt         time.Time
	Create            bool
	AllowSameRevision bool
}

type SharedMutationReceipt struct {
	OperationID string `json:"operation_id"`
	EntityType  string `json:"entity_type"`
	EntityID    string `json:"entity_id"`
	Revision    int64  `json:"revision"`
	Committed   bool   `json:"committed"`
	Reused      bool   `json:"reused"`
}

type OutboxEntry struct {
	ID            string
	EntityType    string
	EntityID      string
	Revision      int64
	Kind          string
	Payload       []byte
	CreatedAt     string
	PublishedAt   string
	Attempts      int64
	NextAttemptAt string
	LastError     string
}

type SharedADRCreate struct {
	OperationID          string
	ProjectID            string
	ProjectCode          string
	InitialNextADRNumber int64
	Kind                 string
	CreatedAt            time.Time
	BuildPayload         func(string) ([]byte, error)
}

// SharedTaskCreate is the local allocation unit for task/create. The payload
// builder is called with the SQLite-owned compact task ID while the sequence,
// task row, and outbox row are committed in one batch.
type SharedTaskCreate struct {
	OperationID           string
	ProjectID             string
	ProjectCode           string
	InitialNextTaskNumber int64
	Kind                  string
	CreatedAt             time.Time
	BuildPayload          func(string) ([]byte, error)
}

type SharedTask struct {
	ID        string
	Revision  int64
	Payload   []byte
	UpdatedAt string
}

type SharedEntity struct {
	ID        string
	Revision  int64
	Payload   []byte
	UpdatedAt string
}

type SharedBootstrapMarker struct {
	ProjectID   string
	HubRevision string
	CompletedAt string
}

var sharedEntityTables = map[string]string{
	"milestone":             "shared_milestones",
	"track":                 "shared_tracks",
	"task":                  "shared_tasks",
	"adr":                   "shared_adrs",
	"rule":                  "shared_rules",
	"journal":               "shared_journals",
	"project_configuration": "shared_project_configurations",
}

var sharedProjectionTables = map[string]string{
	"milestone":             "shared_milestones",
	"track":                 "shared_tracks",
	"task":                  "shared_tasks",
	"adr":                   "shared_adrs",
	"rule":                  "shared_rules",
	"journal":               "shared_journals",
	"project_configuration": "shared_project_configurations",
}

// sharedEvidenceTables maps retired entity types to their preserved tables.
// Reads resolve through it so historical Train/Attempt records stay readable
// as evidence; no write path consults it.
var sharedEvidenceTables = map[string]string{
	"train":               "shared_trains",
	"integration_receipt": "shared_integration_receipts",
}

func (d *Databases) CommitSharedMutation(ctx context.Context, mutation SharedMutation) (SharedMutationReceipt, error) {
	table, ok := sharedEntityTables[mutation.EntityType]
	if !ok {
		return SharedMutationReceipt{}, fmt.Errorf("unsupported shared entity type %q", mutation.EntityType)
	}
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, fmt.Errorf("shared store is unavailable")
	}
	if mutation.OperationID == "" || mutation.EntityID == "" || mutation.Kind == "" {
		return SharedMutationReceipt{}, fmt.Errorf("shared mutation identity is incomplete")
	}
	validRevision := mutation.ExpectedRevision >= 0 && mutation.Revision == mutation.ExpectedRevision+1
	if mutation.AllowSameRevision {
		validRevision = mutation.ExpectedRevision >= 1 && mutation.Revision == mutation.ExpectedRevision
	}
	if !validRevision {
		return SharedMutationReceipt{}, fmt.Errorf("shared mutation revision is not a single CAS step")
	}
	if len(mutation.Payload) == 0 {
		return SharedMutationReceipt{}, fmt.Errorf("shared mutation payload is empty")
	}
	created := mutation.CreatedAt.UTC().Format(time.RFC3339Nano)
	if mutation.CreatedAt.IsZero() {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if existing, found, err := d.outboxEntry(ctx, mutation.OperationID); err != nil {
		return SharedMutationReceipt{}, err
	} else if found {
		return d.reuseSharedMutation(mutation, existing)
	}

	entitySQL := fmt.Sprintf("INSERT INTO %s(id,revision,payload,updated_at) VALUES(?,?,?,?)", table)
	if !mutation.Create {
		entitySQL = fmt.Sprintf("UPDATE %s SET revision=?, payload=?, updated_at=? WHERE id=? AND revision=?", table)
	}
	entityArgs := []any{mutation.EntityID, mutation.Revision, mutation.Payload, created}
	if !mutation.Create {
		entityArgs = []any{mutation.Revision, mutation.Payload, created, mutation.EntityID, mutation.ExpectedRevision}
	}
	_, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: entitySQL, Args: entityArgs, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{mutation.OperationID, mutation.EntityType, mutation.EntityID, mutation.Revision, mutation.Kind, mutation.Payload, created}, RequireRowsAffected: 1},
	})
	if err != nil {
		if existing, found, readErr := d.outboxEntry(ctx, mutation.OperationID); readErr == nil && found {
			return d.reuseSharedMutation(mutation, existing)
		}
		return SharedMutationReceipt{}, err
	}
	return SharedMutationReceipt{
		OperationID: mutation.OperationID,
		EntityType:  mutation.EntityType,
		EntityID:    mutation.EntityID,
		Revision:    mutation.Revision,
		Committed:   true,
	}, nil
}

// CommitSharedTaskCreate allocates a task number locally and commits the
// sequence advance, task entity, and Hub outbox entry atomically. It never
// contacts Hub; a later publisher owns synchronization of the outbox entry.
func (d *Databases) CommitSharedTaskCreate(ctx context.Context, request SharedTaskCreate) (SharedMutationReceipt, string, []byte, error) {
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared store is unavailable")
	}
	if request.OperationID == "" || request.ProjectID == "" || request.Kind == "" || request.BuildPayload == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared task create identity is incomplete")
	}
	if len(request.ProjectCode) != 3 || strings.ToUpper(request.ProjectCode) != request.ProjectCode {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("invalid shared task project code")
	}
	created := request.CreatedAt.UTC().Format(time.RFC3339Nano)
	if request.CreatedAt.IsZero() {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	for attempt := 0; attempt < 8; attempt++ {
		if existing, found, err := d.outboxEntry(ctx, request.OperationID); err != nil {
			return SharedMutationReceipt{}, "", nil, err
		} else if found {
			receipt, err := d.reuseSharedMutation(SharedMutation{
				OperationID: request.OperationID,
				EntityType:  "task",
				EntityID:    existing.EntityID,
				Revision:    existing.Revision,
				Kind:        request.Kind,
				Payload:     existing.Payload,
			}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), err
		}
		next, err := d.nextTaskNumber(ctx, request.ProjectID, request.ProjectCode, request.InitialNextTaskNumber)
		if err != nil {
			return SharedMutationReceipt{}, "", nil, err
		}
		entityID := fmt.Sprintf("%s-TSK%d", request.ProjectCode, next)
		payload, err := request.BuildPayload(entityID)
		if err != nil {
			return SharedMutationReceipt{}, "", nil, err
		}
		if len(payload) == 0 {
			return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared task payload is empty")
		}
		_, err = d.Shared.Batch(ctx, []upstream.Statement{
			{SQL: `UPDATE shared_entity_sequences SET next_number=? WHERE entity_type='task' AND project_id=? AND project_code=? AND next_number=?`, Args: []any{next + 1, request.ProjectID, request.ProjectCode, next}, RequireRowsAffected: 1},
			{SQL: `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{entityID, 1, payload, created}, RequireRowsAffected: 1},
			{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{request.OperationID, "task", entityID, 1, request.Kind, payload, created}, RequireRowsAffected: 1},
		})
		if err == nil {
			return SharedMutationReceipt{
				OperationID: request.OperationID,
				EntityType:  "task",
				EntityID:    entityID,
				Revision:    1,
				Committed:   true,
			}, entityID, payload, nil
		}
		if existing, found, readErr := d.outboxEntry(ctx, request.OperationID); readErr == nil && found {
			receipt, reuseErr := d.reuseSharedMutation(SharedMutation{
				OperationID: request.OperationID,
				EntityType:  "task",
				EntityID:    existing.EntityID,
				Revision:    existing.Revision,
				Kind:        request.Kind,
				Payload:     existing.Payload,
			}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
		}
		if !errors.Is(err, upstream.ErrRowsAffectedMismatch) {
			return SharedMutationReceipt{}, "", nil, err
		}
	}
	return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared task sequence changed during allocation")
}

func (d *Databases) outboxEntry(ctx context.Context, id string) (OutboxEntry, bool, error) {
	rows, err := d.Shared.Query(ctx, `SELECT id,entity_type,entity_id,revision,kind,payload,created_at,COALESCE(published_at,''),attempts,COALESCE(next_attempt_at,''),COALESCE(last_error,'') FROM hub_outbox WHERE id=?`, id)
	if err != nil {
		return OutboxEntry{}, false, err
	}
	if len(rows.Rows) == 0 {
		return OutboxEntry{}, false, nil
	}
	entry, err := decodeOutboxRow(rows.Rows[0])
	return entry, true, err
}

type OutboxRowDecodeError struct {
	Field  string
	Reason string
}

func (e OutboxRowDecodeError) Error() string {
	return fmt.Sprintf("invalid Hub outbox %s: %s", e.Field, e.Reason)
}

func outboxRowTypeError(field string, value any) error {
	return OutboxRowDecodeError{
		Field:  field,
		Reason: fmt.Sprintf("unsupported SQLite value type %T", value),
	}
}

func decodeOutboxRow(row []any) (OutboxEntry, error) {
	if len(row) != 11 {
		return OutboxEntry{}, OutboxRowDecodeError{
			Field:  "row",
			Reason: fmt.Sprintf("expected 11 columns, got %d", len(row)),
		}
	}
	revision, ok := row[3].(int64)
	if !ok {
		return OutboxEntry{}, outboxRowTypeError("revision", row[3])
	}
	var payload []byte
	switch value := row[5].(type) {
	case []byte:
		payload = append([]byte(nil), value...)
	case string:
		payload = []byte(value)
	default:
		return OutboxEntry{}, outboxRowTypeError("payload", row[5])
	}
	values := make([]string, 0, 4)
	for _, field := range []struct {
		index int
		name  string
	}{{index: 0, name: "id"}, {index: 1, name: "entity_type"}, {index: 2, name: "entity_id"}, {index: 4, name: "kind"}} {
		value, ok := row[field.index].(string)
		if !ok {
			return OutboxEntry{}, outboxRowTypeError(field.name, row[field.index])
		}
		values = append(values, value)
	}
	created, ok := row[6].(string)
	if !ok {
		return OutboxEntry{}, outboxRowTypeError("created_at", row[6])
	}
	published, ok := row[7].(string)
	if !ok {
		return OutboxEntry{}, outboxRowTypeError("published_at", row[7])
	}
	attempts, ok := row[8].(int64)
	if !ok {
		return OutboxEntry{}, outboxRowTypeError("attempts", row[8])
	}
	nextAttempt, ok := row[9].(string)
	if !ok {
		return OutboxEntry{}, outboxRowTypeError("next_attempt_at", row[9])
	}
	lastError, ok := row[10].(string)
	if !ok {
		return OutboxEntry{}, outboxRowTypeError("last_error", row[10])
	}
	return OutboxEntry{
		ID:            values[0],
		EntityType:    values[1],
		EntityID:      values[2],
		Revision:      revision,
		Kind:          values[3],
		Payload:       payload,
		CreatedAt:     created,
		PublishedAt:   published,
		Attempts:      attempts,
		NextAttemptAt: nextAttempt,
		LastError:     lastError,
	}, nil
}
