package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func (d *Databases) CommitSharedLifecycleRevision(ctx context.Context, request SharedLifecycleRevision) (SharedMutationReceipt, error) {
	definition, ok := sharedLifecycle(request.EntityType)
	if !ok || definition.HistoryTable == "" {
		return SharedMutationReceipt{}, fmt.Errorf("shared lifecycle %q has no revision history", request.EntityType)
	}
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, fmt.Errorf("shared store is unavailable")
	}
	if request.OperationID == "" || request.ProjectID == "" || request.EntityID == "" || request.Kind == "" || request.ExpectedRevision < 1 || request.ExpectedStoreRevision < 1 || request.Revision != request.ExpectedRevision+1 || len(request.Payload) == 0 || request.Actor == "" || !validSharedHistoryReason(request.Reason) {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s revision", request.EntityType)
	}
	if request.HistoryMutationKind == "" {
		request.HistoryMutationKind = request.Kind
	}
	payloadRevision, err := sharedPayloadLogicalRevision(request.Payload)
	if err != nil || payloadRevision != request.Revision {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s logical revision", request.EntityType)
	}
	currentRows, err := d.Shared.Query(ctx, fmt.Sprintf("SELECT payload FROM %s WHERE id=?", definition.StateTable), request.EntityID)
	if err != nil || len(currentRows.Rows) != 1 {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s current state", request.EntityType)
	}
	currentPayload, ok := currentRows.Rows[0][0].([]byte)
	if !ok {
		return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s current payload", request.EntityType)
	}
	if err := validateSharedLifecycleStatus(definition, currentPayload, request.Payload, false); err != nil {
		return SharedMutationReceipt{}, err
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
	statements := make([]upstream.Statement, 0, 4)
	if seed := request.PreviousHistory; seed != nil {
		if seed.Revision != request.ExpectedRevision || seed.MutationKind == "" || seed.Actor == "" || !validSharedHistoryReason(seed.Reason) || len(seed.Payload) == 0 || seed.RecordedAt == "" {
			return SharedMutationReceipt{}, fmt.Errorf("invalid shared %s history seed", request.EntityType)
		}
		seedFields, marshalErr := json.Marshal(seed.ChangedFields)
		if marshalErr != nil {
			return SharedMutationReceipt{}, marshalErr
		}
		statements = append(statements, sharedHistorySeedStatement(definition, request.EntityID, request.ProjectID, seed, seedFields))
	}
	statements = append(statements,
		upstream.Statement{SQL: fmt.Sprintf("UPDATE %s SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=? AND COALESCE(CAST(json_extract(payload, '$.revision') AS INTEGER), 1)=?", definition.StateTable), Args: []any{request.Revision, request.Payload, recorded, request.EntityID, request.ExpectedStoreRevision, request.ExpectedRevision}, RequireRowsAffected: 1},
		sharedHistoryInsertStatement(definition, request.EntityID, request.ProjectID, request.Revision, request.HistoryMutationKind, request.Actor, request.Reason, changedFields, request.Payload, recorded),
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

func sharedPayloadLogicalRevision(payload []byte) (int64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 0, err
	}
	raw, ok := fields["revision"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return 1, nil
	}
	var revision int64
	if err := json.Unmarshal(raw, &revision); err != nil || revision < 1 {
		return 0, fmt.Errorf("invalid logical revision")
	}
	return revision, nil
}

func validSharedHistoryReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && len([]byte(reason)) <= 1024 && !strings.ContainsAny(reason, "\x00\r\n")
}

func sharedHistoryInsertStatement(definition sharedLifecycleDefinition, entityID, projectID string, revision int64, mutationKind, actor, reason string, changedFields, payload []byte, recordedAt string) upstream.Statement {
	return upstream.Statement{
		SQL:  fmt.Sprintf("INSERT INTO %s(%s,%s,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)", definition.HistoryTable, definition.HistoryEntityColumn, definition.HistoryIDColumn),
		Args: []any{definition.EntityType, entityID, projectID, revision, mutationKind, actor, reason, changedFields, payload, recordedAt}, RequireRowsAffected: 1,
	}
}

func sharedHistorySeedStatement(definition sharedLifecycleDefinition, entityID, projectID string, seed *SharedHistorySeed, changedFields []byte) upstream.Statement {
	return upstream.Statement{
		SQL:  fmt.Sprintf("INSERT INTO %s(%s,%s,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(%s,%s,revision) DO UPDATE SET project_id=excluded.project_id,mutation_kind=excluded.mutation_kind,actor=excluded.actor,reason=excluded.reason,changed_fields=excluded.changed_fields,payload=excluded.payload,recorded_at=excluded.recorded_at WHERE %s.project_id=excluded.project_id AND %s.mutation_kind=excluded.mutation_kind AND %s.actor=excluded.actor AND %s.reason=excluded.reason AND %s.changed_fields=excluded.changed_fields AND %s.payload=excluded.payload AND %s.recorded_at=excluded.recorded_at", definition.HistoryTable, definition.HistoryEntityColumn, definition.HistoryIDColumn, definition.HistoryEntityColumn, definition.HistoryIDColumn, definition.HistoryTable, definition.HistoryTable, definition.HistoryTable, definition.HistoryTable, definition.HistoryTable, definition.HistoryTable, definition.HistoryTable),
		Args: []any{definition.EntityType, entityID, projectID, seed.Revision, seed.MutationKind, seed.Actor, seed.Reason, changedFields, seed.Payload, seed.RecordedAt}, RequireRowsAffected: 1,
	}
}
