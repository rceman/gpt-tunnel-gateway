package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func rev7ServiceADR(t *testing.T) (*Service, string) {
	t.Helper()
	s, revision, _ := testServiceWithoutIdentifiers(t)
	attachTSK409SharedDurability(t, s)
	revision = adoptAuthoringIdentifiersForTest(t, s, revision)
	return s, revision
}

func rev7CreateADR(t *testing.T, s *Service, revision, title string) string {
	t.Helper()
	created, err := s.ADRCreate(context.Background(), ADRCreateInput{
		ADR: model.ADR{ProjectID: "example", Title: title, Summary: "Bounded " + title + " summary", Status: model.ADRStatusProposed, Context: "context", Decision: "decision", Consequences: "consequences"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created.EntityKey
}

func TestTSK409Rev7ADRStatusOnlyUpdateAndArchiveKeepContentRevision(t *testing.T) {
	s, revision := rev7ServiceADR(t)
	ctx := context.Background()
	id := rev7CreateADR(t, s, revision, "Revision stable decision")

	accepted := model.ADRStatusAccepted
	updated, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Status:    &accepted,
		Reason:    "accept decision",
		UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 1 {
		t.Fatalf("status-only update revision=%d want 1", updated.Revision)
	}
	current, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.ADRStatusAccepted || current.Revision != 1 {
		t.Fatalf("current ADR=%#v", current)
	}
	historical, err := s.ADRReadRevision(ctx, "example", id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if historical.Status != model.ADRStatusProposed {
		t.Fatalf("historical ADR=%#v", historical)
	}
	history, err := s.ADRHistory(ctx, "example", id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 2 || history.Items[1].MutationKind != "status" || history.Items[1].Revision != 1 {
		t.Fatalf("ADR history=%#v", history.Items)
	}
	archived, err := s.ADRArchiveCurrent(ctx, ADRArchiveInput{
		ProjectID:  "example",
		ADRID:      id,
		Reason:     "archive decision",
		ArchivedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if archived.Revision != 1 {
		t.Fatalf("archive revision=%d want 1", archived.Revision)
	}
	current, err = s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.ADRStatusArchived || current.Revision != 1 || current.ArchivedAt == nil {
		t.Fatalf("archived ADR=%#v", current)
	}
	if _, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Title:     strPtr("Archived rewrite"),
		Reason:    "rewrite archived",
		UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("archived ADR update was accepted")
	}
	history, err = s.ADRHistory(ctx, "example", id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 3 || history.Items[2].MutationKind != "archive" || history.Items[2].Revision != 1 {
		t.Fatalf("archived ADR history=%#v", history.Items)
	}
}

func TestTSK409Rev7ADRContentAndStatusTransitionIncrementsOnce(t *testing.T) {
	s, revision := rev7ServiceADR(t)
	ctx := context.Background()
	id := rev7CreateADR(t, s, revision, "Combined decision")
	accepted := model.ADRStatusAccepted
	updated, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Title:     strPtr("Combined decision revised"),
		Status:    &accepted,
		Reason:    "content and status",
		UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("content+status revision=%d want 2", updated.Revision)
	}
	current, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != 2 || current.Status != model.ADRStatusAccepted || current.Title != "Combined decision revised" {
		t.Fatalf("combined ADR=%#v", current)
	}
	history, err := s.ADRHistory(ctx, "example", id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 3 || history.Items[1].Revision != 2 || history.Items[1].MutationKind != "update" || history.Items[2].Revision != 2 || history.Items[2].MutationKind != "status" {
		t.Fatalf("combined ADR history=%#v", history.Items)
	}
	entries, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	transitions := 0
	for _, entry := range entries {
		if entry.EntityType == "adr" && entry.Kind == "adr-update" {
			transitions++
			if entry.Revision != 2 {
				t.Fatalf("combined outbox entry=%#v", entry)
			}
		}
	}
	if transitions != 1 {
		t.Fatalf("combined transition outbox entries=%d want 1", transitions)
	}
}

func TestTSK409Rev7ADRTransitionSurvivesDegradedHub(t *testing.T) {
	s, revision := rev7ServiceADR(t)
	ctx := context.Background()
	id := rev7CreateADR(t, s, revision, "Degraded hub decision")
	entries, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := s.publishSharedOutboxEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
		if err := s.Durability.MarkOutboxPublished(ctx, entry.ID, s.durableNow()); err != nil {
			t.Fatal(err)
		}
	}
	healthy := s.Hub.Config.Hub.RepositoryURL
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "missing.git")
	accepted := model.ADRStatusAccepted
	updated, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Status:    &accepted,
		Reason:    "accept under degraded hub",
		UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatalf("degraded Hub blocked the durable transition: %v", err)
	}
	if updated.Revision != 1 {
		t.Fatalf("degraded Hub transition revision=%d want 1", updated.Revision)
	}
	current, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.ADRStatusAccepted || current.Revision != 1 {
		t.Fatalf("degraded Hub current ADR=%#v", current)
	}
	entries, err = s.Durability.PendingOutbox(ctx, 32)
	if err != nil || len(entries) != 1 {
		t.Fatalf("degraded Hub pending outbox=%#v err=%v", entries, err)
	}
	if err := s.publishSharedOutboxEntry(ctx, entries[0]); err == nil {
		t.Fatal("degraded Hub publication unexpectedly succeeded")
	}
	if err := s.Durability.MarkOutboxRetry(ctx, entries[0].ID, s.durableNow(), errSharedOutboxNoop); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil || len(retry) != 1 || retry[0].ID != entries[0].ID {
		t.Fatalf("degraded Hub retry entry=%#v err=%v", retry, err)
	}
	s.Hub.Config.Hub.RepositoryURL = healthy
	if err := s.publishSharedOutboxEntry(ctx, retry[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.MarkOutboxPublished(ctx, retry[0].ID, s.durableNow()); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil || len(pending) != 0 {
		t.Fatalf("recovered outbox=%#v err=%v", pending, err)
	}
}

func TestTSK409Rev7ADRSameValueUpdateIsNoOp(t *testing.T) {
	s, revision := rev7ServiceADR(t)
	ctx := context.Background()
	id := rev7CreateADR(t, s, revision, "No-op decision")
	before, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.ADRHistory(ctx, "example", id, 0)
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	sameTitle, sameSummary, sameContext, sameDecision, sameConsequences := before.Title, before.Summary, before.Context, before.Decision, before.Consequences
	sameStatus := before.Status
	noop, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID:    "example",
		ADRID:        id,
		Title:        &sameTitle,
		Summary:      &sameSummary,
		Context:      &sameContext,
		Decision:     &sameDecision,
		Consequences: &sameConsequences,
		Status:       &sameStatus,
		Reason:       "same values",
		UpdatedBy:    "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if noop.Revision != 1 || noop.Status != "unchanged" {
		t.Fatalf("same-value update result=%#v", noop)
	}
	statusOnly := before.Status
	noop, err = s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Status:    &statusOnly,
		Reason:    "same status",
		UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if noop.Revision != 1 || noop.Status != "unchanged" {
		t.Fatalf("same-status update result=%#v", noop)
	}
	after, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || !after.UpdatedAt.Equal(before.UpdatedAt) || after.LastReason != before.LastReason {
		t.Fatalf("same-value update mutated state: before=%#v after=%#v", before, after)
	}
	afterHistory, err := s.ADRHistory(ctx, "example", id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterHistory.Items) != len(history.Items) {
		t.Fatalf("same-value update added history: before=%d after=%d", len(history.Items), len(afterHistory.Items))
	}
	afterOutbox, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterOutbox) != len(outbox) {
		t.Fatalf("same-value update added outbox entries: before=%d after=%d", len(outbox), len(afterOutbox))
	}
	events, err := s.Durability.ListSharedLifecycleEvents(ctx, "adr", "example", id, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("same-value update recorded events=%#v err=%v", events, err)
	}
}

func TestTSK409Rev7ADRRevisionOneStatusTransitionExposesUpdate(t *testing.T) {
	s, revision := rev7ServiceADR(t)
	ctx := context.Background()
	id := rev7CreateADR(t, s, revision, "Timestamp decision")
	created, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	if !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Fatalf("fresh ADR timestamps differ: %#v", created)
	}
	accepted := model.ADRStatusAccepted
	if _, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Status:    &accepted,
		Reason:    "accept timestamp decision",
		UpdatedBy: "planner",
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 1 || updated.Status != model.ADRStatusAccepted {
		t.Fatalf("revision-1 transition=%#v", updated)
	}
	if updated.UpdatedAt.IsZero() || updated.UpdatedAt.Equal(updated.CreatedAt) || !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Fatalf("revision-1 transition timestamps=%#v", updated)
	}
	if updated.LastReason != "accept timestamp decision" {
		t.Fatalf("revision-1 transition reason=%q", updated.LastReason)
	}
}

func TestTSK409Rev7ADRCorruptOrDisallowedTransitionFailsClosed(t *testing.T) {
	s, revision := rev7ServiceADR(t)
	ctx := context.Background()
	id := rev7CreateADR(t, s, revision, "Corrupt status decision")
	corrupt, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := s.Durability.ReadSharedEntity(ctx, "adr", id)
	if err != nil {
		t.Fatal(err)
	}
	healthyPayload := append([]byte(nil), healthy.Payload...)
	corrupt.Status = "bogus"
	corruptPayload, err := json.Marshal(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(ctx, `UPDATE shared_adrs SET payload=? WHERE id=?`, corruptPayload, id); err != nil {
		t.Fatal(err)
	}
	accepted := model.ADRStatusAccepted
	if _, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Status:    &accepted,
		Reason:    "corrupt transition",
		UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("corrupt stored status was accepted by the ADR update path")
	}
	before, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ADRArchiveCurrent(ctx, ADRArchiveInput{
		ProjectID:  "example",
		ADRID:      id,
		Reason:     "corrupt archive",
		ArchivedBy: "planner",
	}); err == nil {
		t.Fatal("corrupt stored status was accepted by the ADR archive path")
	}
	after, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil || len(after) != len(before) {
		t.Fatalf("corrupt transition changed outbox: before=%d after=%d err=%v", len(before), len(after), err)
	}
	events, err := s.Durability.ListSharedLifecycleEvents(ctx, "adr", "example", id, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("corrupt transition recorded events=%#v err=%v", events, err)
	}

	if _, err := s.Durability.Shared.Exec(ctx, `UPDATE shared_adrs SET payload=? WHERE id=?`, healthyPayload, id); err != nil {
		t.Fatal(err)
	}
	restored, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	restored.Status = model.ADRStatusSuperseded
	restoredPayload, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(ctx, `UPDATE shared_adrs SET payload=? WHERE id=?`, restoredPayload, id); err != nil {
		t.Fatal(err)
	}
	proposed := model.ADRStatusProposed
	if _, err := s.ADRUpdateCurrent(ctx, ADRUpdateInput{
		ProjectID: "example",
		ADRID:     id,
		Status:    &proposed,
		Reason:    "disallowed transition",
		UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("unregistered ADR status transition was accepted")
	}
	state, err := s.Durability.ReadSharedEntity(ctx, "adr", id)
	if err != nil || string(state.Payload) != string(restoredPayload) {
		t.Fatalf("disallowed transition mutated state: %#v err=%v", state, err)
	}
}

func TestTSK409Rev7ADRLegacyHistoricalTitleRemainsReadable(t *testing.T) {
	s, revision := rev7ServiceADR(t)
	ctx := context.Background()
	id := rev7CreateADR(t, s, revision, "Legacy title decision")
	current, err := s.ADRRead(ctx, "example", id)
	if err != nil {
		t.Fatal(err)
	}
	legacy := current
	legacy.Title = strings.Repeat("l", 200)
	legacy.Summary = ""
	legacy.Revision = 1
	legacy.RevisionCount = 1
	legacyPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(ctx, `UPDATE shared_entity_revisions SET payload=? WHERE entity_type='adr' AND entity_id=? AND revision=1`, legacyPayload, id); err != nil {
		t.Fatal(err)
	}
	historical, err := s.ADRReadRevision(ctx, "example", id, 1)
	if err != nil {
		t.Fatalf("legacy historical ADR read failed: %v", err)
	}
	if len([]rune(historical.Title)) != 200 || historical.Summary != "" {
		t.Fatalf("legacy historical ADR=%#v", historical)
	}
	if _, err := s.ADRRead(ctx, "example", id); err != nil {
		t.Fatalf("current ADR read failed after legacy historical read: %v", err)
	}
	history, err := s.ADRHistory(ctx, "example", id, 0)
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("legacy historical ADR history=%#v err=%v", history.Items, err)
	}
}

func strPtr(value string) *string {
	return &value
}
