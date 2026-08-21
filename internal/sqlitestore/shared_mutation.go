package sqlitestore

import (
	"bytes"
	"context"
	"fmt"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

// SharedMutation is the local-first commit unit for one syncable entity. The
// entity CAS and its Hub outbox row are committed by one SQLite batch before a
// caller acknowledges the mutation. Hub synchronization is deliberately not
// part of this operation.
type SharedMutation struct {
	OperationID      string
	EntityType       string
	EntityID         string
	ExpectedRevision int64
	Revision         int64
	Kind             string
	Payload          []byte
	CreatedAt        time.Time
	Create           bool
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
	if mutation.ExpectedRevision < 0 || mutation.Revision != mutation.ExpectedRevision+1 {
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
