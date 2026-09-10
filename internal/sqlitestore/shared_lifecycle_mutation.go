package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

type SharedLifecycleCreate struct {
	OperationID         string
	EntityType          string
	ProjectID           string
	ProjectCode         string
	InitialNextNumber   int64
	Kind                string
	HistoryMutationKind string
	Actor               string
	Reason              string
	ChangedFields       []string
	CreatedAt           time.Time
	BuildPayload        func(string) ([]byte, error)
}

type SharedHistorySeed struct {
	Revision      int64
	MutationKind  string
	Actor         string
	Reason        string
	ChangedFields []string
	Payload       []byte
	RecordedAt    string
}

type SharedLifecycleRevision struct {
	OperationID           string
	EntityType            string
	ProjectID             string
	EntityID              string
	ExpectedRevision      int64
	ExpectedStoreRevision int64
	Revision              int64
	Kind                  string
	HistoryMutationKind   string
	Payload               []byte
	Actor                 string
	Reason                string
	ChangedFields         []string
	CreatedAt             time.Time
	PreviousHistory       *SharedHistorySeed
}

// SharedLifecycleArchive reuses the same CAS, immutable-history, and outbox
// transaction as every other lifecycle revision while making the archive
// transition explicit at the shared boundary.
type SharedLifecycleArchive struct {
	SharedLifecycleRevision
}

type SharedRevisionRecord struct {
	EntityID      string
	ProjectID     string
	Revision      int64
	MutationKind  string
	Actor         string
	Reason        string
	ChangedFields []string
	Payload       []byte
	RecordedAt    string
}

type SharedHistoryPage struct {
	Records      []SharedRevisionRecord
	NextRevision int64
	HasMore      bool
}

func (d *Databases) CommitSharedLifecycleArchive(ctx context.Context, request SharedLifecycleArchive) (SharedMutationReceipt, error) {
	request.Kind = "archive"
	request.HistoryMutationKind = "archive"
	return d.CommitSharedLifecycleRevision(ctx, request.SharedLifecycleRevision)
}

func (d *Databases) CommitSharedLifecycleCreate(ctx context.Context, request SharedLifecycleCreate) (SharedMutationReceipt, string, []byte, error) {
	for attempt := 0; attempt < 8; attempt++ {
		receipt, entityID, payload, err := d.commitSharedLifecycleCreateOnce(ctx, request)
		if err == nil || !errors.Is(err, upstream.ErrRowsAffectedMismatch) {
			return receipt, entityID, payload, err
		}
	}
	return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared %s sequence changed during allocation", request.EntityType)
}

func (d *Databases) commitSharedLifecycleCreateOnce(ctx context.Context, request SharedLifecycleCreate) (SharedMutationReceipt, string, []byte, error) {
	definition, ok := sharedLifecycle(request.EntityType)
	if !ok || definition.SequenceTable == "" {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared lifecycle %q has no allocator", request.EntityType)
	}
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared store is unavailable")
	}
	if request.OperationID == "" || request.ProjectID == "" || request.Kind == "" || request.BuildPayload == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared lifecycle create identity is incomplete")
	}
	if len(request.ProjectCode) != 3 || strings.ToUpper(request.ProjectCode) != request.ProjectCode {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("invalid shared %s project code", request.EntityType)
	}
	if definition.HistoryTable != "" {
		if request.Actor == "" {
			request.Actor = "server"
		}
		if request.Reason == "" {
			request.Reason = "init"
		}
		if !validSharedHistoryReason(request.Reason) {
			return SharedMutationReceipt{}, "", nil, fmt.Errorf("invalid shared %s reason", request.EntityType)
		}
		if request.HistoryMutationKind == "" {
			request.HistoryMutationKind = "create"
		}
	}
	created := request.CreatedAt.UTC().Format(time.RFC3339Nano)
	if request.CreatedAt.IsZero() {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if existing, found, err := d.outboxEntry(ctx, request.OperationID); err != nil {
		return SharedMutationReceipt{}, "", nil, err
	} else if found {
		receipt, reuseErr := d.reuseSharedMutation(SharedMutation{
			OperationID: request.OperationID,
			EntityType:  request.EntityType,
			EntityID:    existing.EntityID,
			Revision:    existing.Revision,
			Kind:        request.Kind,
			Payload:     existing.Payload,
		}, existing)
		return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
	}
	next, err := d.nextSharedLifecycleNumber(ctx, definition, request.ProjectID, request.ProjectCode, request.InitialNextNumber)
	if err != nil {
		return SharedMutationReceipt{}, "", nil, err
	}
	entityID := fmt.Sprintf("%s-%s%d", request.ProjectCode, definition.IDToken, next)
	payload, err := request.BuildPayload(entityID)
	if err != nil {
		return SharedMutationReceipt{}, "", nil, err
	}
	if len(payload) == 0 {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared %s payload is empty", request.EntityType)
	}
	if err := validateSharedLifecycleStatus(definition, nil, payload, true); err != nil {
		return SharedMutationReceipt{}, "", nil, err
	}
	statements := []upstream.Statement{
		{SQL: fmt.Sprintf("UPDATE %s SET %s=? WHERE %s=? AND project_id=? AND %s=? AND %s=?", definition.SequenceTable, definition.SequenceNumberColumn, definition.SequenceEntityColumn, definition.SequenceCodeColumn, definition.SequenceNumberColumn), Args: []any{next + 1, request.EntityType, request.ProjectID, request.ProjectCode, next}, RequireRowsAffected: 1},
		{SQL: fmt.Sprintf("INSERT INTO %s(id,revision,payload,updated_at) VALUES(?,?,?,?)", definition.StateTable), Args: []any{entityID, 1, payload, created}, RequireRowsAffected: 1},
	}
	if definition.HistoryTable != "" {
		changedFields, marshalErr := json.Marshal(request.ChangedFields)
		if marshalErr != nil {
			return SharedMutationReceipt{}, "", nil, marshalErr
		}
		statements = append(statements, sharedHistoryInsertStatement(definition, entityID, request.ProjectID, 1, request.HistoryMutationKind, request.Actor, request.Reason, changedFields, payload, created))
	}
	statements = append(statements, upstream.Statement{
		SQL:  `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`,
		Args: []any{request.OperationID, request.EntityType, entityID, 1, request.Kind, payload, created}, RequireRowsAffected: 1,
	})
	if _, err := d.Shared.Batch(ctx, statements); err != nil {
		if existing, found, readErr := d.outboxEntry(ctx, request.OperationID); readErr == nil && found {
			receipt, reuseErr := d.reuseSharedMutation(SharedMutation{
				OperationID: request.OperationID,
				EntityType:  request.EntityType,
				EntityID:    existing.EntityID,
				Revision:    existing.Revision,
				Kind:        request.Kind,
				Payload:     existing.Payload,
			}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
		}
		return SharedMutationReceipt{}, "", nil, err
	}
	return SharedMutationReceipt{
		OperationID: request.OperationID,
		EntityType:  request.EntityType,
		EntityID:    entityID,
		Revision:    1,
		Committed:   true,
	}, entityID, payload, nil
}
