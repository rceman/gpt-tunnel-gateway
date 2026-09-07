package sqlitestore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"
)

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
	return SharedEntity{
		ID:        id,
		Revision:  revision,
		Payload:   append([]byte(nil), payload...),
		UpdatedAt: updatedAt,
	}, nil
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
		ID:        receipt.ID,
		Revision:  receipt.Revision,
		Payload:   receipt.Payload,
		UpdatedAt: receipt.UpdatedAt,
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
		entities = append(entities, SharedEntity{
			ID:        id,
			Revision:  revision,
			Payload:   append([]byte(nil), payload...),
			UpdatedAt: updatedAt,
		})
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
	return SharedMutationReceipt{
		OperationID: mutation.OperationID,
		EntityType:  existing.EntityType,
		EntityID:    existing.EntityID,
		Revision:    existing.Revision,
		Committed:   true,
		Reused:      true,
	}, nil
}

func (d *Databases) PendingOutbox(ctx context.Context, limit int) ([]OutboxEntry, error) {
	if d == nil || d.Shared == nil {
		return nil, fmt.Errorf("shared store is unavailable")
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid outbox limit")
	}
	rows, err := d.Shared.Query(ctx, `SELECT id,entity_type,entity_id,revision,kind,payload,created_at,COALESCE(published_at,''),attempts,COALESCE(next_attempt_at,''),COALESCE(last_error,'') FROM hub_outbox WHERE published_at IS NULL AND (next_attempt_at IS NULL OR next_attempt_at='' OR next_attempt_at<=?) ORDER BY created_at,id LIMIT ?`, time.Now().UTC().Format(time.RFC3339Nano), limit)
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
	return SharedSyncHealth{
		State:     state,
		Pending:   int(pending),
		Retrying:  int(retrying),
		LastError: last,
	}, nil
}
