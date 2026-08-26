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
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
	TrainAdmissions   []SharedTrainTaskAdmission
	ReplicaIntents    []SharedReplicaIntent
}

// SharedTrainTaskAdmission is committed with the Train CAS and outbox row.
// The primary key reserves a Task for one project Train, while the database
// trigger rejects a competing Train inside the same SQLite transaction.
type SharedTrainTaskAdmission struct {
	ProjectID string
	TaskID    string
	TrainID   string
}

// SharedReplicaIntent is an opaque, typed Hub projection queued atomically
// with its Shared authority transition. The outbox worker performs the slow
// replica publication after the local commit has acknowledged.
type SharedReplicaIntent struct {
	Kind      string
	EntityID  string
	ProjectID string
	Payload   []byte
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
	ProjectID     string
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

type SharedJournalCreate struct {
	OperationID            string
	ProjectID              string
	ProjectCode            string
	InitialNextEventNumber int64
	SupersedesEventID      string
	Kind                   string
	CreatedAt              time.Time
	BuildPayload           func(string) ([]byte, error)
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

type SharedIntegrationReceipt struct {
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

// ReadSharedProjectIdentifiers returns the local allocator projection. It is
// intentionally independent of Hub and is used by synchronous lifecycle
// admission; Hub only receives the resulting outbox publication.
func (d *Databases) ReadSharedProjectIdentifiers(ctx context.Context, projectID, projectCode string) (model.ProjectIdentifiers, error) {
	if d == nil || d.Shared == nil {
		return model.ProjectIdentifiers{}, fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	nextTask, nextADR := int64(1), int64(1)
	found := false
	for _, q := range []struct {
		sql  string
		args []any
		dst  *int64
	}{
		{`SELECT next_task_number FROM shared_task_sequences WHERE project_id=?`, []any{projectID}, &nextTask},
		{`SELECT next_adr_number FROM shared_adr_sequences WHERE project_id=?`, []any{projectID}, &nextADR},
	} {
		rows, err := d.Shared.Query(ctx, q.sql, q.args...)
		if err != nil {
			return model.ProjectIdentifiers{}, err
		}
		if len(rows.Rows) == 1 && len(rows.Rows[0]) == 1 {
			if value, ok := rows.Rows[0][0].(int64); ok && value > 0 {
				found = true
				*q.dst = value
			}
		}
	}
	if !found {
		return model.ProjectIdentifiers{}, fmt.Errorf("Shared project identifiers %q: %w", projectID, os.ErrNotExist)
	}
	result := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: projectID, ProjectCode: projectCode, NextTaskNumber: uint64(nextTask), NextADRNumber: uint64(nextADR)}
	if err := model.ValidateProjectIdentifiers(result); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	return result, nil
}

var sharedEntityTables = map[string]string{
	"task":                  "shared_tasks",
	"train":                 "shared_trains",
	"adr":                   "shared_adrs",
	"rule":                  "shared_rules",
	"journal":               "shared_journals",
	"integration_receipt":   "shared_integration_receipts",
	"project_configuration": "shared_project_configurations",
	"agent":                 "shared_agents",
	"watcher_guide":         "shared_watcher_guides",
	"integration_operation": "shared_integration_operations",
}

var sharedProjectionTables = map[string]string{
	"task":                  "shared_tasks",
	"train":                 "shared_trains",
	"adr":                   "shared_adrs",
	"rule":                  "shared_rules",
	"journal":               "shared_journals",
	"integration_receipt":   "shared_integration_receipts",
	"project_configuration": "shared_project_configurations",
	"agent":                 "shared_agents",
	"watcher_guide":         "shared_watcher_guides",
	"integration_operation": "shared_integration_operations",
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
	statements := []upstream.Statement{
		{SQL: entitySQL, Args: entityArgs, RequireRowsAffected: 1},
	}
	for _, admission := range mutation.TrainAdmissions {
		if admission.ProjectID == "" || admission.TaskID == "" || admission.TrainID == "" {
			return SharedMutationReceipt{}, fmt.Errorf("shared Train admission identity is incomplete")
		}
		statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_train_task_admissions(project_id,task_id,train_id,created_at) VALUES(?,?,?,?) ON CONFLICT(project_id,task_id) DO NOTHING`, Args: []any{admission.ProjectID, admission.TaskID, admission.TrainID, created}})
	}
	for _, intent := range mutation.ReplicaIntents {
		if intent.Kind == "" || intent.EntityID == "" || intent.ProjectID == "" || len(intent.Payload) == 0 {
			return SharedMutationReceipt{}, fmt.Errorf("shared replica intent is incomplete")
		}
		intentID := mutation.OperationID + "::replica::" + intent.Kind + "::" + intent.EntityID
		statements = append(statements, upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{intentID, intent.Kind, intent.EntityID, intent.ProjectID, mutation.Revision, mutation.Kind, intent.Payload, created}, RequireRowsAffected: 1})
	}
	statements = append(statements, upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{mutation.OperationID, mutation.EntityType, mutation.EntityID, "", mutation.Revision, mutation.Kind, mutation.Payload, created}, RequireRowsAffected: 1})
	_, err := d.Shared.Batch(ctx, statements)
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

func (d *Databases) CommitSharedADRCreate(ctx context.Context, request SharedADRCreate) (SharedMutationReceipt, string, []byte, error) {
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared store is unavailable")
	}
	if request.OperationID == "" || request.ProjectID == "" || request.Kind == "" || request.BuildPayload == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared ADR create identity is incomplete")
	}
	if len(request.ProjectCode) != 3 || strings.ToUpper(request.ProjectCode) != request.ProjectCode {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("invalid shared ADR project code")
	}
	created := request.CreatedAt.UTC().Format(time.RFC3339Nano)
	if request.CreatedAt.IsZero() {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if existing, found, err := d.outboxEntry(ctx, request.OperationID); err != nil {
		return SharedMutationReceipt{}, "", nil, err
	} else if found {
		receipt, reuseErr := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "adr", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
		return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
	}
	next, err := d.nextADRNumber(ctx, request.ProjectID, request.ProjectCode, request.InitialNextADRNumber)
	if err != nil {
		return SharedMutationReceipt{}, "", nil, err
	}
	id := fmt.Sprintf("%s-ADR%d", request.ProjectCode, next)
	payload, err := request.BuildPayload(id)
	if err != nil {
		return SharedMutationReceipt{}, "", nil, err
	}
	if len(payload) == 0 {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared ADR payload is empty")
	}
	_, err = d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_adr_sequences SET next_adr_number=? WHERE project_id=? AND project_code=? AND next_adr_number=?`, Args: []any{next + 1, request.ProjectID, request.ProjectCode, next}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{id, 1, payload, created}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{request.OperationID, "adr", id, 1, request.Kind, payload, created}, RequireRowsAffected: 1},
	})
	if err != nil {
		if existing, found, readErr := d.outboxEntry(ctx, request.OperationID); readErr == nil && found {
			receipt, reuseErr := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "adr", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
		}
		return SharedMutationReceipt{}, "", nil, err
	}
	return SharedMutationReceipt{OperationID: request.OperationID, EntityType: "adr", EntityID: id, Revision: 1, Committed: true}, id, payload, nil
}

// CommitSharedJournalCreate allocates a journal ID, records a supersession
// guard, and publishes the event to the Hub outbox in one local transaction.
// Hub synchronization is deliberately left to the outbox worker.
func (d *Databases) CommitSharedJournalCreate(ctx context.Context, request SharedJournalCreate) (SharedMutationReceipt, string, []byte, error) {
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared store is unavailable")
	}
	if request.OperationID == "" || request.ProjectID == "" || request.Kind == "" || request.BuildPayload == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared journal create identity is incomplete")
	}
	if len(request.ProjectCode) != 3 || strings.ToUpper(request.ProjectCode) != request.ProjectCode {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("invalid shared journal project code")
	}
	d.journalMu.Lock()
	defer d.journalMu.Unlock()
	created := request.CreatedAt.UTC().Format(time.RFC3339Nano)
	if request.CreatedAt.IsZero() {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	for attempt := 0; attempt < 8; attempt++ {
		if existing, found, err := d.outboxEntry(ctx, request.OperationID); err != nil {
			return SharedMutationReceipt{}, "", nil, err
		} else if found {
			receipt, err := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "journal", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), err
		}
		if _, err := d.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_journal_sequences(project_id,project_code,next_event_number) VALUES(?,?,?)`, request.ProjectID, request.ProjectCode, maxInt64(request.InitialNextEventNumber, 1)); err != nil {
			return SharedMutationReceipt{}, "", nil, err
		}
		rows, err := d.Shared.Query(ctx, `SELECT project_code,next_event_number FROM shared_journal_sequences WHERE project_id=?`, request.ProjectID)
		if err != nil {
			return SharedMutationReceipt{}, "", nil, err
		}
		if len(rows.Rows) != 1 || rows.Rows[0][0] != request.ProjectCode {
			return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared journal project code mismatch")
		}
		next, ok := rows.Rows[0][1].(int64)
		if !ok || next < 1 || uint64(next) > model.MaxSafeInteger {
			return SharedMutationReceipt{}, "", nil, fmt.Errorf("invalid shared journal sequence")
		}
		entityID, err := model.FormatJournalID(request.ProjectCode, uint64(next))
		if err != nil {
			return SharedMutationReceipt{}, "", nil, err
		}
		payload, err := request.BuildPayload(entityID)
		if err != nil {
			return SharedMutationReceipt{}, "", nil, err
		}
		if len(payload) == 0 {
			return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared journal payload is empty")
		}
		nextSequence := next
		if uint64(next) < model.MaxSafeInteger {
			nextSequence++
		}
		statements := []upstream.Statement{
			{SQL: `UPDATE shared_journal_sequences SET next_event_number=? WHERE project_id=? AND project_code=? AND next_event_number=?`, Args: []any{nextSequence, request.ProjectID, request.ProjectCode, next}, RequireRowsAffected: 1},
			{SQL: `INSERT INTO shared_journals(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{entityID, 1, payload, created}, RequireRowsAffected: 1},
		}
		if request.SupersedesEventID != "" {
			statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_journal_supersessions(target_id,operation_id,created_at) VALUES(?,?,?)`, Args: []any{request.SupersedesEventID, request.OperationID, created}, RequireRowsAffected: 1})
		}
		statements = append(statements, upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{request.OperationID, "journal", entityID, 1, request.Kind, payload, created}, RequireRowsAffected: 1})
		if _, err = d.Shared.Batch(ctx, statements); err == nil {
			return SharedMutationReceipt{OperationID: request.OperationID, EntityType: "journal", EntityID: entityID, Revision: 1, Committed: true}, entityID, payload, nil
		}
		if existing, found, readErr := d.outboxEntry(ctx, request.OperationID); readErr == nil && found {
			receipt, reuseErr := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "journal", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
		}
		if !errors.Is(err, upstream.ErrRowsAffectedMismatch) {
			return SharedMutationReceipt{}, "", nil, err
		}
	}
	return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared journal sequence changed during allocation")
}

func maxInt64(value, fallback int64) int64 {
	if value > fallback {
		return value
	}
	return fallback
}

func (d *Databases) PutSharedJournalSequence(ctx context.Context, projectID, projectCode string, next int64) error {
	if d == nil || d.Shared == nil || projectID == "" || len(projectCode) != 3 || strings.ToUpper(projectCode) != projectCode || next < 1 {
		return fmt.Errorf("invalid shared journal sequence")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_journal_sequences(project_id,project_code,next_event_number) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET project_code=excluded.project_code,next_event_number=CASE WHEN excluded.next_event_number > shared_journal_sequences.next_event_number THEN excluded.next_event_number ELSE shared_journal_sequences.next_event_number END`, projectID, projectCode, next)
	return err
}

func (d *Databases) ReadSharedJournalSequence(ctx context.Context, projectID string) (string, int64, bool, error) {
	if d == nil || d.Shared == nil {
		return "", 0, false, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_event_number FROM shared_journal_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return "", 0, false, err
	}
	if len(rows.Rows) == 0 {
		return "", 0, false, nil
	}
	if len(rows.Rows) != 1 {
		return "", 0, false, fmt.Errorf("invalid shared journal sequence")
	}
	code, codeOK := rows.Rows[0][0].(string)
	next, nextOK := rows.Rows[0][1].(int64)
	if !codeOK || !nextOK || next < 1 {
		return "", 0, false, fmt.Errorf("invalid shared journal sequence")
	}
	return code, next, true, nil
}

func (d *Databases) PutSharedJournalSupersession(ctx context.Context, targetID, operationID string) error {
	if d == nil || d.Shared == nil || targetID == "" || operationID == "" {
		return fmt.Errorf("invalid shared journal supersession")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_journal_supersessions(target_id,operation_id,created_at) VALUES(?,?,?) ON CONFLICT(target_id) DO NOTHING`, targetID, operationID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (d *Databases) nextADRNumber(ctx context.Context, projectID, projectCode string, initial int64) (int64, error) {
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_adr_number FROM shared_adr_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) == 0 {
		if initial < 1 {
			initial = 1
		}
		if _, err := d.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_adr_sequences(project_id,project_code,next_adr_number) VALUES(?,?,?)`, projectID, projectCode, initial); err != nil {
			return 0, err
		}
		rows, err = d.Shared.Query(ctx, `SELECT project_code,next_adr_number FROM shared_adr_sequences WHERE project_id=?`, projectID)
		if err != nil {
			return 0, err
		}
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode {
		return 0, fmt.Errorf("shared ADR project code mismatch")
	}
	next, ok := rows.Rows[0][1].(int64)
	if !ok || next < 1 {
		return 0, fmt.Errorf("invalid shared ADR sequence")
	}
	return next, nil
}

func (d *Databases) PutSharedADRSequence(ctx context.Context, projectID, projectCode string, next int64) error {
	if d == nil || d.Shared == nil || projectID == "" || len(projectCode) != 3 || strings.ToUpper(projectCode) != projectCode || next < 1 {
		return fmt.Errorf("invalid shared ADR sequence")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_adr_sequences(project_id,project_code,next_adr_number) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET project_code=excluded.project_code,next_adr_number=CASE WHEN excluded.next_adr_number > shared_adr_sequences.next_adr_number THEN excluded.next_adr_number ELSE shared_adr_sequences.next_adr_number END`, projectID, projectCode, next)
	return err
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

func (d *Databases) PutSharedTaskSequence(ctx context.Context, projectID, projectCode string, next int64) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if projectID == "" || len(projectCode) != 3 || strings.ToUpper(projectCode) != projectCode || next < 1 {
		return fmt.Errorf("invalid shared task sequence")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_task_sequences(project_id,project_code,next_task_number) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET project_code=excluded.project_code,next_task_number=CASE WHEN excluded.next_task_number > shared_task_sequences.next_task_number THEN excluded.next_task_number ELSE shared_task_sequences.next_task_number END`, projectID, projectCode, next)
	return err
}

func (d *Databases) MarkSharedBootstrapComplete(ctx context.Context, marker SharedBootstrapMarker) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if marker.ProjectID == "" || marker.HubRevision == "" || marker.CompletedAt == "" {
		return fmt.Errorf("invalid shared bootstrap marker")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_bootstrap_markers(project_id,hub_revision,completed_at) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET hub_revision=excluded.hub_revision,completed_at=excluded.completed_at`, marker.ProjectID, marker.HubRevision, marker.CompletedAt)
	return err
}

// DeleteSharedBootstrapMarker removes one test/recovery marker through the
// typed persistence boundary. Callers do not need to know the table layout.
func (d *Databases) DeleteSharedBootstrapMarker(ctx context.Context, projectID string) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("project id is required")
	}
	_, err := d.Shared.Exec(ctx, `DELETE FROM shared_bootstrap_markers WHERE project_id=?`, projectID)
	return err
}

func (d *Databases) SharedBootstrapComplete(ctx context.Context, projectID string) (bool, error) {
	if d == nil || d.Shared == nil {
		return false, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_id,hub_revision,completed_at FROM shared_bootstrap_markers WHERE project_id=?`, projectID)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) == 0 {
		return false, nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return false, fmt.Errorf("invalid shared bootstrap marker")
	}
	project, projectOK := rows.Rows[0][0].(string)
	revision, revisionOK := rows.Rows[0][1].(string)
	completed, completedOK := rows.Rows[0][2].(string)
	if !projectOK || !revisionOK || !completedOK || project != projectID || revision == "" || completed == "" {
		return false, fmt.Errorf("invalid shared bootstrap marker")
	}
	return true, nil
}

func (d *Databases) ReadSharedBootstrapMarker(ctx context.Context, projectID string) (SharedBootstrapMarker, error) {
	if d == nil || d.Shared == nil {
		return SharedBootstrapMarker{}, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_id,hub_revision,completed_at FROM shared_bootstrap_markers WHERE project_id=?`, projectID)
	if err != nil {
		return SharedBootstrapMarker{}, err
	}
	if len(rows.Rows) == 0 {
		return SharedBootstrapMarker{}, fmt.Errorf("Shared bootstrap marker %q: %w", projectID, os.ErrNotExist)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return SharedBootstrapMarker{}, fmt.Errorf("invalid Shared bootstrap marker")
	}
	project, ok1 := rows.Rows[0][0].(string)
	revision, ok2 := rows.Rows[0][1].(string)
	completed, ok3 := rows.Rows[0][2].(string)
	if !ok1 || !ok2 || !ok3 || project != projectID {
		return SharedBootstrapMarker{}, fmt.Errorf("invalid Shared bootstrap marker")
	}
	return SharedBootstrapMarker{ProjectID: project, HubRevision: revision, CompletedAt: completed}, nil
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

func (d *Databases) ReadSharedEntity(ctx context.Context, entityType, entityID string) (SharedEntity, error) {
	table, ok := sharedProjectionTables[entityType]
	if !ok {
		return SharedEntity{}, fmt.Errorf("unsupported shared entity type %q", entityType)
	}
	if d == nil || d.Shared == nil {
		return SharedEntity{}, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, fmt.Sprintf(`SELECT id,revision,payload,updated_at FROM %s WHERE id=?`, table), entityID)
	if err != nil {
		return SharedEntity{}, err
	}
	if len(rows.Rows) == 0 {
		return SharedEntity{}, fmt.Errorf("shared %s %q: %w", entityType, entityID, os.ErrNotExist)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 4 {
		return SharedEntity{}, fmt.Errorf("invalid shared %s row", entityType)
	}
	id, idOK := rows.Rows[0][0].(string)
	revision, revisionOK := rows.Rows[0][1].(int64)
	payload, payloadOK := rows.Rows[0][2].([]byte)
	updatedAt, updatedOK := rows.Rows[0][3].(string)
	if !idOK || !revisionOK || !payloadOK || !updatedOK {
		return SharedEntity{}, fmt.Errorf("invalid shared %s row", entityType)
	}
	return SharedEntity{ID: id, Revision: revision, Payload: append([]byte(nil), payload...), UpdatedAt: updatedAt}, nil
}

func SharedIntegrationReceiptID(projectID, trainID string) string {
	return projectID + "\x00" + trainID
}

func (d *Databases) PutSharedProjection(ctx context.Context, entityType string, entity SharedEntity) error {
	table, ok := sharedProjectionTables[entityType]
	if !ok {
		return fmt.Errorf("unsupported shared projection type %q", entityType)
	}
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if entity.ID == "" || entity.Revision < 1 || len(entity.Payload) == 0 || entity.UpdatedAt == "" {
		return fmt.Errorf("invalid shared projection")
	}
	_, err := d.Shared.Exec(ctx, fmt.Sprintf(`INSERT INTO %s(id,revision,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,payload=excluded.payload,updated_at=excluded.updated_at WHERE excluded.revision >= %s.revision`, table, table), entity.ID, entity.Revision, entity.Payload, entity.UpdatedAt)
	return err
}

func (d *Databases) PutSharedIntegrationReceipt(ctx context.Context, receipt SharedIntegrationReceipt) error {
	return d.PutSharedProjection(ctx, "integration_receipt", SharedEntity{
		ID: receipt.ID, Revision: receipt.Revision, Payload: receipt.Payload, UpdatedAt: receipt.UpdatedAt,
	})
}

func (d *Databases) ListSharedEntities(ctx context.Context, entityType string, limit int) ([]SharedEntity, error) {
	return d.listSharedEntitiesQuery(ctx, entityType, `SELECT id,revision,payload,updated_at FROM %s ORDER BY updated_at,id LIMIT ?`, limit)
}

func (d *Databases) ListSharedEntitiesPage(ctx context.Context, entityType string, offset, limit int) ([]SharedEntity, error) {
	return d.listSharedEntitiesQuery(ctx, entityType, `SELECT id,revision,payload,updated_at FROM %s ORDER BY updated_at,id LIMIT ? OFFSET ?`, limit, offset)
}

func (d *Databases) ListSharedEntitiesAfter(ctx context.Context, entityType, afterUpdatedAt, afterID string, limit int) ([]SharedEntity, error) {
	if afterUpdatedAt == "" && afterID == "" {
		return d.listSharedEntitiesQuery(ctx, entityType, `SELECT id,revision,payload,updated_at FROM %s ORDER BY updated_at,id LIMIT ?`, limit)
	}
	return d.listSharedEntitiesQuery(ctx, entityType, `SELECT id,revision,payload,updated_at FROM %s WHERE updated_at > ? OR (updated_at = ? AND id > ?) ORDER BY updated_at,id LIMIT ?`, afterUpdatedAt, afterUpdatedAt, afterID, limit)
}

func (d *Databases) listSharedEntitiesQuery(ctx context.Context, entityType, queryFormat string, args ...any) ([]SharedEntity, error) {
	table, ok := sharedEntityTables[entityType]
	if !ok {
		return nil, fmt.Errorf("unsupported shared entity type %q", entityType)
	}
	if d == nil || d.Shared == nil {
		return nil, fmt.Errorf("shared store is unavailable")
	}
	var limit int
	switch len(args) {
	case 1:
		var ok bool
		limit, ok = args[0].(int)
		if !ok {
			return nil, fmt.Errorf("invalid shared entity limit")
		}
	case 2:
		var ok bool
		limit, ok = args[0].(int)
		if !ok {
			return nil, fmt.Errorf("invalid shared entity limit")
		}
		if offset, ok := args[1].(int); !ok || offset < 0 {
			return nil, fmt.Errorf("invalid shared entity offset")
		}
	case 4:
		var ok bool
		limit, ok = args[3].(int)
		if !ok {
			return nil, fmt.Errorf("invalid shared entity limit")
		}
	default:
		return nil, fmt.Errorf("invalid shared entity limit")
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid shared entity limit")
	}
	queryArgs := append([]any(nil), args...)
	query := fmt.Sprintf(queryFormat, table)
	rows, err := d.Shared.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, err
	}
	entities := make([]SharedEntity, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid shared %s row", entityType)
		}
		id, idOK := row[0].(string)
		revision, revisionOK := row[1].(int64)
		payload, payloadOK := row[2].([]byte)
		updatedAt, updatedOK := row[3].(string)
		if !idOK || !revisionOK || !payloadOK || !updatedOK {
			return nil, fmt.Errorf("invalid shared %s row", entityType)
		}
		entities = append(entities, SharedEntity{ID: id, Revision: revision, Payload: append([]byte(nil), payload...), UpdatedAt: updatedAt})
	}
	return entities, nil
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
	rows, err := d.Shared.Query(ctx, `SELECT id,entity_type,entity_id,project_id,revision,kind,payload,created_at,COALESCE(published_at,''),attempts,COALESCE(next_attempt_at,''),COALESCE(last_error,'') FROM hub_outbox WHERE published_at IS NULL AND (next_attempt_at IS NULL OR next_attempt_at='' OR next_attempt_at<=?) ORDER BY created_at,id LIMIT ?`, time.Now().UTC().Format(time.RFC3339Nano), limit)
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

func (d *Databases) ReadSharedOutboxEntry(ctx context.Context, operationID string) (OutboxEntry, bool, error) {
	if operationID == "" {
		return OutboxEntry{}, false, fmt.Errorf("shared outbox operation id is required")
	}
	return d.outboxEntry(ctx, operationID)
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

func (d *Databases) MarkOutboxRetry(ctx context.Context, id string, at time.Time, cause error) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if id == "" {
		return fmt.Errorf("outbox id is required")
	}
	message := "outbox publish failed"
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 512 {
		message = message[:512]
	}
	_, err := d.Shared.Exec(ctx, `UPDATE hub_outbox SET attempts=attempts+1,next_attempt_at=?,last_error=? WHERE id=? AND published_at IS NULL`, at.UTC().Format(time.RFC3339Nano), message, id)
	return err
}

type SharedSyncHealth struct {
	State     string `json:"state"`
	Pending   int    `json:"pending"`
	Retrying  int    `json:"retrying"`
	LastError string `json:"last_error,omitempty"`
}

func (d *Databases) SharedSyncHealth(ctx context.Context) (SharedSyncHealth, error) {
	if d == nil || d.Shared == nil {
		return SharedSyncHealth{}, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN attempts>0 THEN 1 ELSE 0 END),0),COALESCE((SELECT last_error FROM hub_outbox WHERE published_at IS NULL AND last_error<>'' ORDER BY created_at DESC LIMIT 1),'') FROM hub_outbox WHERE published_at IS NULL`)
	if err != nil {
		return SharedSyncHealth{}, err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return SharedSyncHealth{}, fmt.Errorf("invalid shared sync health row")
	}
	pending, ok := rows.Rows[0][0].(int64)
	if !ok {
		return SharedSyncHealth{}, fmt.Errorf("invalid shared sync pending count")
	}
	retrying, ok := rows.Rows[0][1].(int64)
	if !ok {
		return SharedSyncHealth{}, fmt.Errorf("invalid shared sync retry count")
	}
	last, ok := rows.Rows[0][2].(string)
	if !ok {
		return SharedSyncHealth{}, fmt.Errorf("invalid shared sync error")
	}
	state := "healthy"
	if pending > 0 {
		state = "pending"
	}
	if last != "" {
		state = "degraded"
	}
	return SharedSyncHealth{State: state, Pending: int(pending), Retrying: int(retrying), LastError: last}, nil
}

func (d *Databases) outboxEntry(ctx context.Context, id string) (OutboxEntry, bool, error) {
	rows, err := d.Shared.Query(ctx, `SELECT id,entity_type,entity_id,project_id,revision,kind,payload,created_at,COALESCE(published_at,''),attempts,COALESCE(next_attempt_at,''),COALESCE(last_error,'') FROM hub_outbox WHERE id=?`, id)
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
	if len(row) != 12 {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox row")
	}
	revision, ok := row[4].(int64)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox revision")
	}
	payload, ok := row[6].([]byte)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox payload")
	}
	values := make([]string, 0, 4)
	for _, index := range []int{0, 1, 2, 3, 5} {
		value, ok := row[index].(string)
		if !ok {
			return OutboxEntry{}, fmt.Errorf("invalid Hub outbox text field")
		}
		values = append(values, value)
	}
	created, ok := row[7].(string)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox created_at")
	}
	published, ok := row[8].(string)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox published_at")
	}
	attempts, ok := row[9].(int64)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox attempts")
	}
	nextAttempt, ok := row[10].(string)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox next attempt")
	}
	lastError, ok := row[11].(string)
	if !ok {
		return OutboxEntry{}, fmt.Errorf("invalid Hub outbox last error")
	}
	return OutboxEntry{ID: values[0], EntityType: values[1], EntityID: values[2], ProjectID: values[3], Revision: revision, Kind: values[4], Payload: payload, CreatedAt: created, PublishedAt: published, Attempts: attempts, NextAttemptAt: nextAttempt, LastError: lastError}, nil
}
