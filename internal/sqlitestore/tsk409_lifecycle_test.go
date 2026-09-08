package sqlitestore

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestTSK409FreshV12SchemaAndRevisionOneHistory(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, table := range []string{"shared_entity_sequences", "shared_entity_revisions"} {
		rows, err := db.Shared.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		if err != nil || len(rows.Rows) != 1 {
			t.Fatalf("table %s missing: rows=%v err=%v", table, rows.Rows, err)
		}
	}
	rows, err := db.Shared.Query(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND name=?", "shared_entity_revisions_project_idx")
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("revision index missing: rows=%v err=%v", rows.Rows, err)
	}
	payload := []byte(`{"id":"ADR-EXM-1","project_id":"example","revision":1}`)
	record := SharedRevisionRecord{
		EntityID:      "ADR-EXM-1",
		ProjectID:     "example",
		Revision:      1,
		MutationKind:  "create",
		Actor:         "planner",
		Reason:        "create",
		ChangedFields: []string{"title"},
		Payload:       payload,
		RecordedAt:    "2026-09-07T00:00:00Z",
	}
	if err := db.EnsureSharedLifecycleHistory(ctx, "adr", record); err != nil {
		t.Fatal(err)
	}
	got, err := db.ReadSharedRevision(ctx, "adr", "example", record.EntityID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ChangedFields, record.ChangedFields) || string(got.Payload) != string(payload) {
		t.Fatalf("history=%#v want=%#v", got, record)
	}
}

func TestTSK409HistorySeedIsIdempotentAndConflictSafe(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	record := SharedRevisionRecord{
		EntityID:      "ADR-EXM-2",
		ProjectID:     "example",
		Revision:      1,
		MutationKind:  "migration",
		Actor:         "migration",
		Reason:        "migration",
		ChangedFields: []string{"migration"},
		Payload:       []byte(`{"id":"ADR-EXM-2"}`),
		RecordedAt:    "2026-09-07T00:00:00Z",
	}
	if err := db.EnsureSharedLifecycleHistory(ctx, "adr", record); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSharedLifecycleHistory(ctx, "adr", record); err != nil {
		t.Fatalf("identical seed was not idempotent: %v", err)
	}
	conflict := record
	conflict.Reason = "different"
	if err := db.EnsureSharedLifecycleHistory(ctx, "adr", conflict); err == nil {
		t.Fatal("conflicting immutable history seed succeeded")
	}
	var decoded map[string]any
	if err := json.Unmarshal(record.Payload, &decoded); err != nil {
		t.Fatal(err)
	}
}
