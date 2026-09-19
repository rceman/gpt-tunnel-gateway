package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type journalTestFixture struct {
	service *Service
	db      *sqlitestore.Databases
}

func newJournalTestFixture(t *testing.T) journalTestFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlitestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewWithDurabilityDeferredWorkers(config.Config{
		StateDir: dir,
		Projects: map[string]config.ProjectConfig{"example": {AirelaySessionKey: "fixture"}},
	}, db)
	return journalTestFixture{
		service: s,
		db:      db,
	}
}

func (f journalTestFixture) session(t *testing.T, role string) context.Context {
	t.Helper()
	ref := role + "-ref"
	record, err := durableSession.NewStoreWithDurability(f.db).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: role,
		SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	return WithAgentSessionID(context.Background(), record.ID)
}

func plannerNotesData() json.RawMessage {
	return json.RawMessage(`{"summary":"planning note","decisions":["d1"],"commitments":[],"facts":["f1"],"assumptions":[],"blockers":[],"unresolved":[],"next_actions":["n1"],"references":["EXM-ADR1"]}`)
}

func workerLessonData() json.RawMessage {
	return json.RawMessage(`{"task":"EXM-TSK1","mistake":"missed the seed","why":"fixture skipped seeding","prevention":"assert seeds in fixture","scope":"project","evidence":["commit:abc"]}`)
}

func leadFrictionData() json.RawMessage {
	return json.RawMessage(`{"problem":"review stalls","evidence":["EXM-OPR7"],"impact":"dispatch latency","proposed_improvement":"batch reviews"}`)
}

func TestJournalContractExposesClosedStreams(t *testing.T) {
	f := newJournalTestFixture(t)
	for _, stream := range []string{"planner-notes", "worker-lessons", "lead-friction"} {
		contract, err := f.service.JournalContract(f.session(t, durableSession.RolePlanner), JournalContractInput{Stream: stream})
		if err != nil {
			t.Fatalf("journal/contract %s: %v", stream, err)
		}
		if contract.Stream != stream || contract.Purpose == "" || contract.Guide == "" || len(contract.WriterAuthority) == 0 || contract.DataSchema == nil {
			t.Fatalf("journal/contract %s returned an incomplete contract: %#v", stream, contract)
		}
	}
	if _, err := f.service.JournalContract(f.session(t, durableSession.RolePlanner), JournalContractInput{Stream: "lead-decisions"}); err == nil {
		t.Fatal("unsupported stream lead-decisions did not fail closed")
	}
	if _, err := f.service.JournalContract(f.session(t, durableSession.RolePlanner), JournalContractInput{Stream: "test-perf-findings"}); err == nil {
		t.Fatal("unsupported stream test-perf-findings did not fail closed")
	}
	lessons, err := f.service.JournalContract(f.session(t, durableSession.RoleWorker), JournalContractInput{Stream: "worker-lessons"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons.WriterAuthority) != 2 || lessons.WriterAuthority[0] != "lead" || lessons.WriterAuthority[1] != "worker" {
		t.Fatalf("worker-lessons writer authority=%v, want lead+worker", lessons.WriterAuthority)
	}
}

func TestJournalAddEnforcesStreamWriterAuthority(t *testing.T) {
	f := newJournalTestFixture(t)
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RolePlanner), JournalAddInput{
		Stream: "planner-notes",
		Data:   plannerNotesData(),
	}); err != nil {
		t.Fatalf("planner planner-notes add: %v", err)
	}
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RoleLead), JournalAddInput{
		Stream: "worker-lessons",
		Data:   workerLessonData(),
	}); err != nil {
		t.Fatalf("lead worker-lessons add: %v", err)
	}
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RoleWorker), JournalAddInput{
		Stream: "worker-lessons",
		Data:   workerLessonData(),
	}); err != nil {
		t.Fatalf("worker worker-lessons add: %v", err)
	}
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RoleLead), JournalAddInput{
		Stream: "lead-friction",
		Data:   leadFrictionData(),
	}); err != nil {
		t.Fatalf("lead lead-friction add: %v", err)
	}
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RoleWorker), JournalAddInput{
		Stream: "planner-notes",
		Data:   plannerNotesData(),
	}); err == nil {
		t.Fatal("worker was allowed to write planner-notes")
	}
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RolePlanner), JournalAddInput{
		Stream: "lead-friction",
		Data:   leadFrictionData(),
	}); err == nil {
		t.Fatal("planner was allowed to write lead-friction")
	}
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RoleWorker), JournalAddInput{
		Stream: "lead-friction",
		Data:   leadFrictionData(),
	}); err == nil {
		t.Fatal("worker was allowed to write lead-friction")
	}
}

func TestJournalAddValidatesContractBeforeCommit(t *testing.T) {
	f := newJournalTestFixture(t)
	ctx := f.session(t, durableSession.RolePlanner)
	cases := []json.RawMessage{
		json.RawMessage(`{"summary":"x"}`), // missing required fields
		json.RawMessage(`{"summary":"x","bogus":1,"decisions":[],"commitments":[],"facts":[],"assumptions":[],"blockers":[],"unresolved":[],"next_actions":[],"references":[]}`), // unknown field
		json.RawMessage(`{"summary":"","decisions":[],"commitments":[],"facts":[],"assumptions":[],"blockers":[],"unresolved":[],"next_actions":[],"references":[]}`),            // empty required string
	}
	for index, data := range cases {
		if _, _, err := f.service.JournalAdd(ctx, JournalAddInput{
			Stream: "planner-notes",
			Data:   data,
		}); err == nil {
			t.Fatalf("case %d: invalid data was accepted", index)
		} else if violations := JournalViolations(err); len(violations) == 0 || violations[0].Path == "" {
			t.Fatalf("case %d: violations have no failing paths: %v", index, err)
		}
	}
	page, err := f.service.JournalList(ctx, JournalListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("invalid writes committed entries: %#v", page.Items)
	}
	if _, _, err := f.service.JournalAdd(f.session(t, durableSession.RoleWorker), JournalAddInput{
		Stream: "worker-lessons",
		Data:   json.RawMessage(`{"mistake":"m","why":"w","prevention":"p","scope":"invalid-scope","evidence":["e"]}`),
	}); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("invalid scope enum was not rejected with a scoped violation: %v", err)
	}
}

func TestJournalAddReadListLifecycle(t *testing.T) {
	f := newJournalTestFixture(t)
	ctx := f.session(t, durableSession.RolePlanner)
	entry, result, err := f.service.JournalAdd(ctx, JournalAddInput{
		Stream: "planner-notes",
		Data:   plannerNotesData(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "EXM-JRN1" || entry.Sequence != 1 || entry.Status != model.JournalStatusPublished || entry.ProjectID != "example" || entry.Role != durableSession.RolePlanner || entry.SessionID == "" {
		t.Fatalf("unexpected journal provenance: %#v", entry)
	}
	if result.Revision != 1 || result.EntityKey != entry.ID {
		t.Fatalf("journal add did not commit revision 1: %#v", result)
	}
	second, _, err := f.service.JournalAdd(f.session(t, durableSession.RoleLead), JournalAddInput{
		Stream: "lead-friction",
		Data:   leadFrictionData(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "EXM-JRN2" || second.Sequence != 2 {
		t.Fatalf("second entry did not advance the server-owned JRN sequence: %#v", second)
	}

	read, err := f.service.JournalRead(ctx, JournalReadInput{Key: entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	if read.Stream != "planner-notes" || string(read.Data) != string(plannerNotesData()) {
		t.Fatalf("journal/read returned a different entry: %#v", read)
	}

	page, err := f.service.JournalList(ctx, JournalListInput{Stream: "planner-notes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != entry.ID {
		t.Fatalf("stream-filtered list=%#v", page.Items)
	}
	page, err = f.service.JournalList(ctx, JournalListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("journal list=%#v, want 2 entries", page.Items)
	}
	if _, err := f.service.JournalRead(ctx, JournalReadInput{Key: "EXM-JRN999"}); err == nil {
		t.Fatal("journal/read on a missing key did not fail")
	}
	if _, err := f.service.JournalRead(ctx, JournalReadInput{Key: "bogus"}); err == nil {
		t.Fatal("journal/read on a malformed key did not fail closed")
	}
}

func TestJournalAddFailsClosedWithoutSessionAuthority(t *testing.T) {
	f := newJournalTestFixture(t)
	if _, _, err := f.service.JournalAdd(context.Background(), JournalAddInput{
		Stream: "planner-notes",
		Data:   plannerNotesData(),
	}); err == nil {
		t.Fatal("journal/add without a session was accepted")
	}
	s := New(config.Config{StateDir: t.TempDir()})
	if _, _, err := s.JournalAdd(context.Background(), JournalAddInput{
		Stream: "planner-notes",
		Data:   plannerNotesData(),
	}); err == nil {
		t.Fatal("journal/add without durability was accepted")
	}
}

func TestJournalEntryIsPublishedOnly(t *testing.T) {
	entry := model.JournalEntry{
		SchemaVersion: model.SchemaVersion, ID: "EXM-JRN1", ProjectID: "example",
		Status: "archived", Stream: "planner-notes", Data: plannerNotesData(),
		Actor: "a", Role: "planner", SessionID: "s", Sequence: 1,
	}
	if err := model.ValidateJournalEntry(entry); err == nil {
		t.Fatal("archived journal status was accepted")
	}
	entry.Status = model.JournalStatusPublished
	entry.CreatedAt = entry.CreatedAt.Add(1) // non-zero
	if err := model.ValidateJournalEntry(entry); err != nil {
		t.Fatalf("published journal entry rejected: %v", err)
	}
}

func TestJournalAddOutboxPublishesToHub(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	s.Durability = db
	ref := "planner-ref"
	record, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner,
		SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := s.JournalAdd(WithAgentSessionID(context.Background(), record.ID), JournalAddInput{
		Stream: "planner-notes",
		Data:   plannerNotesData(),
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingOutbox(context.Background(), 16)
	if err != nil {
		t.Fatal(err)
	}
	published := false
	for _, candidate := range pending {
		if candidate.EntityType != "journal" {
			continue
		}
		published = true
		if candidate.EntityID != entry.ID || candidate.Kind != "journal-add" {
			t.Fatalf("journal outbox entry=%#v", candidate)
		}
		if err := s.publishSharedOutboxEntry(context.Background(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	if !published {
		t.Fatal("journal/add produced no Shared outbox intent")
	}
	var stored model.JournalEntry
	if err := s.Hub.ReadJSON(context.Background(), s.journalPath("example", entry.ID), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ID != entry.ID || stored.Stream != "planner-notes" || stored.Role != durableSession.RolePlanner || stored.SessionID != record.ID {
		t.Fatalf("published journal=%#v", stored)
	}
}
