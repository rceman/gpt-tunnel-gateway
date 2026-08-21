package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCommitSharedMutationCommitsEntityAndOutboxTogether(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	receipt, err := db.CommitSharedMutation(ctx, SharedMutation{
		OperationID:      "OPR-GTW-1",
		EntityType:       "task",
		EntityID:         "TSK-GTW-1",
		ExpectedRevision: 0,
		Revision:         1,
		Kind:             "create",
		Payload:          []byte(`{"title":"local first"}`),
		CreatedAt:        time.Unix(10, 0).UTC(),
		Create:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Committed || receipt.Reused || receipt.Revision != 1 {
		t.Fatalf("receipt=%#v", receipt)
	}

	row, err := db.Shared.Query(ctx, `SELECT revision,payload FROM shared_tasks WHERE id=?`, "TSK-GTW-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Rows) != 1 || row.Rows[0][0] != int64(1) || string(row.Rows[0][1].([]byte)) != `{"title":"local first"}` {
		t.Fatalf("entity row=%#v", row.Rows)
	}
	pending, err := db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "OPR-GTW-1" || pending[0].EntityType != "task" {
		t.Fatalf("pending=%#v", pending)
	}
	if err := db.MarkOutboxPublished(ctx, pending[0].ID, time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("published outbox remains pending=%#v", pending)
	}
}

func TestCommitSharedMutationCASAndOutboxAreAtomic(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	base := SharedMutation{
		OperationID:      "OPR-GTW-2",
		EntityType:       "task",
		EntityID:         "TSK-GTW-2",
		ExpectedRevision: 0,
		Revision:         1,
		Kind:             "create",
		Payload:          []byte("v1"),
		Create:           true,
	}
	if _, err := db.CommitSharedMutation(ctx, base); err != nil {
		t.Fatal(err)
	}
	stale := base
	stale.OperationID = "OPR-GTW-2-STALE"
	stale.ExpectedRevision = 0
	stale.Revision = 1
	stale.Payload = []byte("v2")
	stale.Create = false
	if _, err := db.CommitSharedMutation(ctx, stale); err == nil {
		t.Fatal("stale CAS unexpectedly committed")
	}
	row, err := db.Shared.Query(ctx, `SELECT revision,payload,(SELECT COUNT(*) FROM hub_outbox WHERE id=?) FROM shared_tasks WHERE id=?`, stale.OperationID, stale.EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Rows) != 1 || row.Rows[0][0] != int64(1) || string(row.Rows[0][1].([]byte)) != "v1" || row.Rows[0][2] != int64(0) {
		t.Fatalf("stale mutation was not atomic=%#v", row.Rows)
	}
}

func TestCommitSharedMutationIsIdempotentAndIdentityBound(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mutation := SharedMutation{
		OperationID:      "OPR-GTW-3",
		EntityType:       "adr",
		EntityID:         "ADR-GTW-3",
		ExpectedRevision: 0,
		Revision:         1,
		Kind:             "create",
		Payload:          []byte("adr"),
		Create:           true,
	}
	first, err := db.CommitSharedMutation(ctx, mutation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CommitSharedMutation(ctx, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || first.OperationID != second.OperationID {
		t.Fatalf("retry receipts first=%#v second=%#v", first, second)
	}
	mutation.Payload = []byte("tampered")
	if _, err := db.CommitSharedMutation(ctx, mutation); err == nil {
		t.Fatal("operation identity mismatch was accepted")
	} else if errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
}

func TestCommitSharedMutationRejectsOperationalEventFamilies(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.CommitSharedMutation(context.Background(), SharedMutation{
		OperationID:      "OPR-GTW-EVT",
		EntityType:       "event",
		EntityID:         "EVT-GTW-1",
		ExpectedRevision: 0,
		Revision:         1,
		Kind:             "append",
		Payload:          []byte("event"),
		Create:           true,
	})
	if err == nil {
		t.Fatal("operational event was imported into Shared authority")
	}
}

func TestSharedADRCreatePublishesThroughOutbox(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	receipt, id, payload, err := db.CommitSharedADRCreate(context.Background(), SharedADRCreate{
		OperationID: "OPR-GTW-ADR-1", ProjectID: "example", ProjectCode: "EXM", Kind: "adr-create",
		BuildPayload: func(id string) ([]byte, error) { return []byte(`{"id":"` + id + `"}`), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Committed || id != "EXM-ADR1" || len(payload) == 0 {
		t.Fatalf("receipt=%#v id=%q", receipt, id)
	}
	entries, err := db.PendingOutbox(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntityType != "adr" {
		t.Fatalf("entries=%#v", entries)
	}
}

func TestSharedOutboxRetryBackoffAndHealthSurviveRestart(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CommitSharedMutation(context.Background(), SharedMutation{OperationID: "OPR-GTW-RETRY", EntityType: "task", EntityID: "TSK-GTW-RETRY", ExpectedRevision: 0, Revision: 1, Kind: "create", Payload: []byte("retry"), Create: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkOutboxRetry(context.Background(), "OPR-GTW-RETRY", time.Now().UTC().Add(time.Hour), errors.New("remote unavailable")); err != nil {
		t.Fatal(err)
	}
	health, err := db.SharedSyncHealth(context.Background())
	if err != nil || health.State != "degraded" || health.Pending != 1 || health.Retrying != 1 {
		t.Fatalf("health=%#v err=%v", health, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := db.PendingOutbox(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("backoff was not honored after restart: %#v", entries)
	}
	if err := db.MarkOutboxRetry(context.Background(), "OPR-GTW-RETRY", time.Now().UTC().Add(-time.Second), errors.New("retry now")); err != nil {
		t.Fatal(err)
	}
	entries, err = db.PendingOutbox(context.Background(), 10)
	if err != nil || len(entries) != 1 || entries[0].Attempts != 2 {
		t.Fatalf("retry convergence entries=%#v err=%v", entries, err)
	}
}

func TestSharedSyncHealthEmptyOutboxIsHealthy(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	health, err := db.SharedSyncHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.State != "healthy" || health.Pending != 0 || health.Retrying != 0 || health.LastError != "" {
		t.Fatalf("empty outbox health=%#v", health)
	}
}
