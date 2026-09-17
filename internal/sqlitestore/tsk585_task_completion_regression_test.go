package sqlitestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk585CompletionFixture(t *testing.T) (*Databases, CommitTaskCompletionRequest) {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prev := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "EXM-TSK1", ProjectID: "example",
		Type: model.TaskTypeTask, Title: "Completion task", Summary: "Completion summary.", Objective: "Complete safely.",
		ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		Status: model.TaskAuthoringPlanned, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	prev.RevisionSHA256, _ = model.HashTaskAuthoring(prev)
	prevPayload, err := json.Marshal(prev)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, prev.ID, int64(1), prevPayload, prev.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	final := prev
	final.Status = model.TaskAuthoringDone
	final.UpdatedAt = prev.UpdatedAt.Add(time.Second)
	finalPayload, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	contract := []byte(`{"schema_version":1,"mode":"non_code","reason":"ok","task_revision":1,"task_revision_sha256":"` + prev.RevisionSHA256 + `","acceptance":[{"criterion":1,"evidence":["EXM-JRN1"]}]}`)
	sum := sha256.Sum256(contract)
	opSum := sha256.Sum256(append([]byte("example\x00EXM-TSK1\x00"), contract...))
	return db, CommitTaskCompletionRequest{
		OperationID:         "task-complete-" + hex.EncodeToString(opSum[:]),
		ProjectID:           "example",
		TaskID:              "EXM-TSK1",
		Revision:            1,
		PreviousTaskPayload: prevPayload,
		TaskPayload:         finalPayload,
		FromStatus:          model.TaskAuthoringPlanned,
		Actor:               "planner",
		Reason:              "ok",
		Contract:            contract,
		ContractSHA256:      hex.EncodeToString(sum[:]),
		RecordedAt:          final.UpdatedAt,
	}
}
func TestTSK585TaskCompletionStoreValidation(t *testing.T) {
	db, req := tsk585CompletionFixture(t)
	defer db.Close()
	ctx := context.Background()

	cases := map[string]func(*CommitTaskCompletionRequest){
		"wrong contract digest": func(r *CommitTaskCompletionRequest) { r.ContractSHA256 = strings.Repeat("0", 64) },
		"wrong operation id":    func(r *CommitTaskCompletionRequest) { r.OperationID = "task-complete-" + strings.Repeat("1", 64) },
		"content drift": func(r *CommitTaskCompletionRequest) {
			var drifted model.TaskAuthoring
			_ = json.Unmarshal(r.TaskPayload, &drifted)
			drifted.Title = "drifted"
			raw, _ := json.Marshal(drifted)
			r.TaskPayload = raw
		},
		"final revision drift": func(r *CommitTaskCompletionRequest) {
			var drifted model.TaskAuthoring
			_ = json.Unmarshal(r.TaskPayload, &drifted)
			drifted.Revision++
			raw, _ := json.Marshal(drifted)
			r.TaskPayload = raw
		},
		"bad task id":   func(r *CommitTaskCompletionRequest) { r.TaskID = "bogus" },
		"nul reason":    func(r *CommitTaskCompletionRequest) { r.Reason = "a\x00b" },
		"empty reason":  func(r *CommitTaskCompletionRequest) { r.Reason = "" },
		"unpaired exec": func(r *CommitTaskCompletionRequest) { r.FinalExecution = &model.TaskExecutionState{} },
	}
	for name, mutate := range cases {
		bad := req
		mutate(&bad)
		if err := db.CommitTaskCompletion(ctx, bad); err == nil {
			t.Fatalf("%s must reject", name)
		}
	}
	var task model.TaskAuthoring
	rows, _ := db.Shared.Query(ctx, `SELECT payload FROM shared_tasks WHERE id='EXM-TSK1'`)
	if err := json.Unmarshal(rows.Rows[0][0].([]byte), &task); err != nil || task.Status != model.TaskAuthoringPlanned {
		t.Fatalf("rejected requests must not mutate: %#v", task)
	}
	if err := db.CommitTaskCompletion(ctx, req); err != nil {
		t.Fatalf("valid request must commit: %v", err)
	}
	event, found, err := db.ReadTaskCompletionEvent(ctx, "example", "EXM-TSK1")
	if err != nil || !found || event.OperationID != req.OperationID {
		t.Fatalf("event=%#v found=%v err=%v", event, found, err)
	}
	if _, err := db.ListTaskLifecycleEvents(ctx, "example", "EXM-TSK1", 257); err == nil {
		t.Fatal("list limit must be bounded")
	}
}
func TestTSK585TaskHistoryCursor(t *testing.T) {
	at := time.Date(2026, 9, 12, 10, 0, 0, 123456789, time.UTC).Format(time.RFC3339Nano)
	cursor := SharedLifecycleHistoryCursor{
		RecordedAt: at,
		Source:     1,
		ID:         7,
	}
	if _, err := EncodeSharedLifecycleHistoryCursor(SharedLifecycleHistoryCursor{}); err == nil {
		t.Fatal("zero cursor must not encode")
	}
	raw, err := EncodeSharedLifecycleHistoryCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeSharedLifecycleHistoryCursor(cursor)
	if err != nil || again != raw {
		t.Fatal("cursor encoding must be deterministic")
	}
	decoded, err := DecodeSharedLifecycleHistoryCursor(raw)
	if err != nil || decoded != cursor {
		t.Fatalf("roundtrip=%#v err=%v", decoded, err)
	}
	for name, mutated := range map[string]string{
		"unknown field":   `{"recorded_at":"` + at + `","source":1,"id":7,"extra":true}`,
		"trailing value":  raw + ` {}`,
		"trailing junk":   raw + `x`,
		"noncanonical tz": `{"recorded_at":"2026-09-12T10:00:00.123456789+00:00","source":1,"id":7}`,
		"source range":    `{"recorded_at":"` + at + `","source":2,"id":7}`,
		"bad id":          `{"recorded_at":"` + at + `","source":1,"id":0}`,
	} {
		if _, err := DecodeSharedLifecycleHistoryCursor(mutated); err == nil {
			t.Fatalf("%s must reject", name)
		}
	}
}
func TestTSK621TaskHistoryOrdersByRevisionAcrossClockInversion(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	taskID := "EXM-TSK621"
	fields := []byte(`[]`)
	payload := []byte(`{}`)
	for _, row := range []struct {
		revision int64
		recorded string
	}{
		{revision: 1, recorded: "2026-09-15T10:00:00Z"},
		{revision: 2, recorded: "2026-09-15T10:00:02Z"},
		{revision: 3, recorded: "2026-09-15T10:00:02.000000001Z"},
	} {
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES('task',?,?,?,'update','planner','revise',?,?,?)`, taskID, "example", row.revision, fields, payload, row.recorded); err != nil {
			t.Fatal(err)
		}
	}
	state, err := json.Marshal(map[string]any{"id": taskID, "project_id": "example", "revision": 3, "status": "archived"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, taskID, int64(3), state, "2026-09-15T10:00:02.000000001Z"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		opID         string
		revision     int64
		kind         string
		mutationKind string
		fromStatus   string
		toStatus     string
		recorded     string
	}{
		{opID: "task-history-complete", revision: 1, kind: SharedLifecycleEventKindStatus, mutationKind: "complete", fromStatus: "planned", toStatus: "done", recorded: "2026-09-15T09:00:00Z"},
		{opID: "task-history-archive", revision: 2, kind: SharedLifecycleEventKindArchive, mutationKind: "archive", fromStatus: "done", toStatus: "archived", recorded: "2026-09-15T10:00:02Z"},
	} {
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,CAST('["status"]' AS BLOB))`, row.opID, "task", "example", taskID, row.revision, row.kind, row.fromStatus, row.toStatus, "planner", "history", payload, row.recorded, row.mutationKind); err != nil {
			t.Fatal(err)
		}
	}
	var after SharedLifecycleHistoryCursor
	want := []struct {
		revision int64
		kind     string
		source   int
		recorded string
	}{
		{revision: 1, kind: "update", source: 0, recorded: "2026-09-15T10:00:00Z"},
		{revision: 1, kind: "complete", source: 1, recorded: "2026-09-15T09:00:00Z"},
		{revision: 2, kind: "update", source: 0, recorded: "2026-09-15T10:00:02Z"},
		{revision: 2, kind: "archive", source: 1, recorded: "2026-09-15T10:00:02Z"},
		{revision: 3, kind: "update", source: 0, recorded: "2026-09-15T10:00:02.000000001Z"},
	}
	for i, expected := range want {
		page, pageErr := db.ListSharedLifecycleHistoryPage(ctx, "task", "example", taskID, after, 1)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		if len(page.Records) != 1 || page.Records[0].Revision != expected.revision || page.Records[0].MutationKind != expected.kind || page.Records[0].RecordedAt != expected.recorded {
			t.Fatalf("page %d=%#v want revision=%d kind=%s recorded=%s", i, page, expected.revision, expected.kind, expected.recorded)
		}
		if i == len(want)-1 {
			if page.HasMore || page.NextCursor != "" {
				t.Fatalf("final page=%#v", page)
			}
			break
		}
		cursor, cursorErr := DecodeSharedLifecycleHistoryCursor(page.NextCursor)
		if cursorErr != nil {
			t.Fatal(cursorErr)
		}
		if cursor.Revision != expected.revision || cursor.Source != expected.source {
			t.Fatalf("page %d semantic cursor=%#v", i, cursor)
		}
		after = cursor
	}
}

func TestTSK585TaskLifecycleEventTransitions(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	recorded := time.Now().UTC().Format(time.RFC3339Nano)
	insert := func(op, kind, from, to string) error {
		sharedKind := SharedLifecycleEventKindArchive
		mutationKind := "archive"
		if kind == TaskLifecycleEventKindComplete {
			sharedKind = SharedLifecycleEventKindStatus
			mutationKind = "complete"
		}
		_, err := db.Shared.Exec(ctx, `INSERT INTO shared_lifecycle_events(operation_id,entity_type,project_id,entity_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at,mutation_kind,changed_fields) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,CAST('["status"]' AS BLOB))`, op, "task", "example", "EXM-TSK1", 1, sharedKind, from, to, "planner", "ok", []byte(`{"schema_version":1}`), recorded, mutationKind)
		return err
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES('EXM-TSK1',1,?,?)`, []byte(`{"id":"EXM-TSK1","project_id":"example","revision":1,"status":"archived"}`), recorded); err != nil {
		t.Fatal(err)
	}
	if err := insert("op-archive-done", "archive", "done", "archived"); err != nil {
		t.Fatal(err)
	}
	events, err := db.ListTaskLifecycleEvents(ctx, "example", "EXM-TSK1", 256)
	if err != nil || len(events) != 1 {
		t.Fatalf("archive-from-done must decode: %v %v", events, err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_lifecycle_events`); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string][3]string{
		"complete from done":      {"op-c-done", "complete", "done"},
		"complete to archived":    {"op-c-arch", "complete", "planned"},
		"archive to done":         {"op-a-done", "archive", "planned"},
		"archive from superseded": {"op-a-sup", "archive", "superseded"},
		"unknown kind":            {"op-x", "explode", "planned"},
	} {
		to := map[string]string{
			"op-c-done": "done", "op-c-arch": "archived", "op-a-done": "done",
			"op-a-sup": "archived", "op-x": "done",
		}[tc[0]]
		if err := insert(tc[0], tc[1], tc[2], to); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ListTaskLifecycleEvents(ctx, "example", "EXM-TSK1", 256); err == nil {
			t.Fatalf("%s must fail closed", name)
		}
		if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_lifecycle_events WHERE operation_id=?`, tc[0]); err != nil {
			t.Fatal(err)
		}
	}
}
