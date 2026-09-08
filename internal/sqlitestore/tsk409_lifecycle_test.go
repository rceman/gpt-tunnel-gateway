package sqlitestore

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	upstream "github.com/rceman/go-sqlite-store/store"
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

func TestTSK409V12BackfillsLegacyRowsAndSequenceHighWaterMarks(t *testing.T) {
	state := t.TempDir()
	sharedPath, _ := Paths(state)
	shared, err := upstream.Open(engineConfig(Config{})(sharedPath))
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	ctx := context.Background()
	if err := applyTSK538SharedLegacyPrefix(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_project_identifiers(project_id,project_code,next_task_number,next_adr_number,next_rule_number,next_train_number,next_journal_number) VALUES(?,?,?,?,?,?,?)`, "example", "EXM", 4, 8, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_task_sequences(project_id,project_code,next_task_number) VALUES(?,?,?)`, "example", "EXM", 9); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_adr_sequences(project_id,project_code,next_adr_number) VALUES(?,?,?)`, "example", "EXM", 11); err != nil {
		t.Fatal(err)
	}
	payloadWithRevision := []byte(`{"id":"EXM-ADR1","project_id":"example","revision":3,"created_by":"planner","last_reason":"seed","created_at":"2026-09-07T00:00:00Z"}`)
	payloadWithoutRevision := []byte(`{"id":"EXM-ADR2","project_id":"example","created_by":"planner","created_at":"2026-09-07T00:00:01Z"}`)
	for _, item := range []struct {
		id       string
		payload  []byte
		updated  string
		stateRev int
	}{
		{id: "EXM-ADR1", payload: payloadWithRevision, updated: "2026-09-07T00:00:02Z", stateRev: 99},
		{id: "EXM-ADR2", payload: payloadWithoutRevision, updated: "2026-09-07T00:00:03Z", stateRev: 88},
	} {
		if _, err := shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, item.id, item.stateRev, item.payload, item.updated); err != nil {
			t.Fatal(err)
		}
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	shared = db.Shared
	rows, err := shared.Query(ctx, `SELECT entity_id,revision,changed_fields,payload FROM shared_entity_revisions WHERE entity_type='adr' ORDER BY entity_id`)
	if err != nil || len(rows.Rows) != 2 {
		t.Fatalf("backfilled history rows=%v err=%v", rows.Rows, err)
	}
	if rows.Rows[0][1] != int64(3) || rows.Rows[1][1] != int64(1) {
		t.Fatalf("logical revisions=%v, want 3 and 1", rows.Rows)
	}
	for i, want := range [][]byte{payloadWithRevision, payloadWithoutRevision} {
		var fields string
		switch value := rows.Rows[i][2].(type) {
		case string:
			fields = value
		case []byte:
			fields = string(value)
		}
		if fields != `["migration"]` {
			t.Fatalf("changed_fields[%d]=%v", i, rows.Rows[i][2])
		}
		payload, ok := rows.Rows[i][3].([]byte)
		if !ok || string(payload) != string(want) {
			t.Fatalf("payload[%d] changed", i)
		}
	}
	seq, err := shared.Query(ctx, `SELECT entity_type,next_number FROM shared_entity_sequences WHERE project_id='example' AND entity_type IN ('task','adr') ORDER BY entity_type`)
	if err != nil || len(seq.Rows) != 2 || seq.Rows[0][1] != int64(11) || seq.Rows[1][1] != int64(9) {
		t.Fatalf("sequence high-water rows=%v err=%v", seq.Rows, err)
	}
}
