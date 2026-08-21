package sqlitestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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
	ID          string
	EntityType  string
	EntityID    string
	Revision    int64
	Kind        string
	Payload     []byte
	CreatedAt   string
	PublishedAt string
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

var sharedEntityTables = map[string]string{
	"task":    "shared_tasks",
	"train":   "shared_trains",
	"adr":     "shared_adrs",
	"rule":    "shared_rules",
	"journal": "shared_journals",
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
	return SharedMutationReceipt{OperationID: mutation.OperationID, EntityType: mutation.EntityType, EntityID: mutation.EntityID, Revision: mutation.Revision, Committed: true}, nil
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
			receipt, err := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "task", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
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
			{SQL: `UPDATE shared_task_sequences SET next_task_number=? WHERE project_id=? AND project_code=? AND next_task_number=?`, Args: []any{next + 1, request.ProjectID, request.ProjectCode, next}, RequireRowsAffected: 1},
			{SQL: `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{entityID, 1, payload, created}, RequireRowsAffected: 1},
			{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{request.OperationID, "task", entityID, 1, request.Kind, payload, created}, RequireRowsAffected: 1},
		})
		if err == nil {
			return SharedMutationReceipt{OperationID: request.OperationID, EntityType: "task", EntityID: entityID, Revision: 1, Committed: true}, entityID, payload, nil
		}
		if existing, found, readErr := d.outboxEntry(ctx, request.OperationID); readErr == nil && found {
			receipt, reuseErr := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "task", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
		}
		if !errors.Is(err, upstream.ErrRowsAffectedMismatch) {
			return SharedMutationReceipt{}, "", nil, err
		}
	}
	return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared task sequence changed during allocation")
}

func (d *Databases) nextTaskNumber(ctx context.Context, projectID, projectCode string, initial int64) (int64, error) {
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_task_number FROM shared_task_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) == 0 {
		if initial < 1 {
			initial = 1
		}
		if _, err := d.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_task_sequences(project_id,project_code,next_task_number) VALUES(?,?,?)`, projectID, projectCode, initial); err != nil {
			return 0, err
		}
		rows, err = d.Shared.Query(ctx, `SELECT project_code,next_task_number FROM shared_task_sequences WHERE project_id=?`, projectID)
		if err != nil {
			return 0, err
		}
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode {
		return 0, fmt.Errorf("shared task project code mismatch")
	}
	next, ok := rows.Rows[0][1].(int64)
	if !ok || next < 1 {
		return 0, fmt.Errorf("invalid shared task sequence")
	}
	return next, nil
}

func (d *Databases) ReadSharedTaskSequence(ctx context.Context, projectID string) (string, int64, bool, error) {
	if d == nil || d.Shared == nil {
		return "", 0, false, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_task_number FROM shared_task_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return "", 0, false, err
	}
	if len(rows.Rows) == 0 {
		return "", 0, false, nil
	}
	if len(rows.Rows) != 1 {
		return "", 0, false, fmt.Errorf("invalid shared task sequence")
	}
	code, codeOK := rows.Rows[0][0].(string)
	next, nextOK := rows.Rows[0][1].(int64)
	if !codeOK || !nextOK || next < 1 {
		return "", 0, false, fmt.Errorf("invalid shared task sequence")
	}
	return code, next, true, nil
}

func (d *Databases) ReadSharedTask(ctx context.Context, taskID string) (SharedTask, error) {
	if d == nil || d.Shared == nil {
		return SharedTask{}, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT id,revision,payload,updated_at FROM shared_tasks WHERE id=?`, taskID)
	if err != nil {
		return SharedTask{}, err
	}
	if len(rows.Rows) == 0 {
		return SharedTask{}, fmt.Errorf("shared task %q: %w", taskID, os.ErrNotExist)
	}
	if len(rows.Rows[0]) != 4 {
		return SharedTask{}, fmt.Errorf("invalid shared task row")
	}
	id, idOK := rows.Rows[0][0].(string)
	revision, revisionOK := rows.Rows[0][1].(int64)
	payload, payloadOK := rows.Rows[0][2].([]byte)
	updatedAt, updatedOK := rows.Rows[0][3].(string)
	if !idOK || !revisionOK || !payloadOK || !updatedOK {
		return SharedTask{}, fmt.Errorf("invalid shared task row")
	}
	return SharedTask{ID: id, Revision: revision, Payload: append([]byte(nil), payload...), UpdatedAt: updatedAt}, nil
}

// SeedSharedTask imports a compatibility read into Shared without producing
// an outbox event. It is used only by an async worker before a first local
// mutation when the authoritative Shared snapshot predates the task.
func (d *Databases) SeedSharedTask(ctx context.Context, task SharedTask) error {
	if task.ID == "" || task.Revision < 1 || len(task.Payload) == 0 || task.UpdatedAt == "" {
		return fmt.Errorf("invalid shared task seed")
	}
	_, err := d.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, task.Revision, task.Payload, task.UpdatedAt)
	return err
}

func (d *Databases) reuseSharedMutation(mutation SharedMutation, existing OutboxEntry) (SharedMutationReceipt, error) {
	if existing.EntityType != mutation.EntityType || existing.EntityID != mutation.EntityID || existing.Revision != mutation.Revision || existing.Kind != mutation.Kind || !bytes.Equal(existing.Payload, mutation.Payload) {
		return SharedMutationReceipt{}, fmt.Errorf("shared mutation operation identity mismatch")
	}
	return SharedMutationReceipt{OperationID: mutation.OperationID, EntityType: existing.EntityType, EntityID: existing.EntityID, Revision: existing.Revision, Committed: true, Reused: true}, nil
}

func (d *Databases) PendingOutbox(ctx context.Context, limit int) ([]OutboxEntry, error) {
	if d == nil || d.Shared == nil {
		return nil, fmt.Errorf("shared store is unavailable")
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid outbox limit")
	}
	rows, err := d.Shared.Query(ctx, `SELECT id,entity_type,entity_id,revision,kind,payload,created_at,COALESCE(published_at,'') FROM hub_outbox WHERE published_at IS NULL ORDER BY created_at,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]OutboxEntry, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		entry, err := decodeOutboxRow(row)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (d *Databases) MarkOutboxPublished(ctx context.Context, id string, at time.Time) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if id == "" {
		return fmt.Errorf("outbox id is required")
	}
	_, err := d.Shared.Exec(ctx, `UPDATE hub_outbox SET published_at=? WHERE id=? AND published_at IS NULL`, at.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (d *Databases) outboxEntry(ctx context.Context, id string) (OutboxEntry, bool, error) {
	rows, err := d.Shared.Query(ctx, `SELECT id,entity_type,entity_id,revision,kind,payload,created_at,COALESCE(published_at,'') FROM hub_outbox WHERE id=?`, id)
	if err != nil {
		return OutboxEntry{}, false, err
	}
	if len(rows.Rows) == 0 {
		return OutboxEntry{}, false, nil
	}
	entry, err := decodeOutboxRow(rows.Rows[0])
	return entry, true, err
}

func decodeOutboxRow(row []any) (OutboxEntry, error) {
	if len(row) != 8 {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox row")
	}
	revision, ok := row[3].(int64)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox revision")
	}
	payload, ok := row[5].([]byte)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox payload")
	}
	values := make([]string, 0, 4)
	for _, index := range []int{0, 1, 2, 4} {
		value, ok := row[index].(string)
		if !ok {
			return OutboxEntry{}, fmt.Errorf("invalid Hub outbox text field")
		}
		values = append(values, value)
	}
	created, ok := row[6].(string)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox created_at")
	}
	published, ok := row[7].(string)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox published_at")
	}
	return OutboxEntry{ID: values[0], EntityType: values[1], EntityID: values[2], Revision: revision, Kind: values[3], Payload: payload, CreatedAt: created, PublishedAt: published}, nil
}
