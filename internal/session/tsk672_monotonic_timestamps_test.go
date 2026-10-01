package session

import (
	"errors"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// withSessionClock deterministically replaces the wall clock for the duration
// of fn — no sleeps, no reliance on host timer behavior.
func withSessionClock(t *testing.T, fn func(set func(time.Time))) {
	t.Helper()
	old := sessionNowFunc
	current := time.Now().UTC()
	sessionNowFunc = func() time.Time { return current }
	t.Cleanup(func() { sessionNowFunc = old })
	fn(func(value time.Time) { current = value })
}

// TestTSK672SessionTimestampsSurviveWallClockSlew reproduces the TSK670
// verification flake deterministically: a backward wall-clock step between
// create and touch/update/end must not regress durable ordering.
func TestTSK672SessionTimestampsSurviveWallClockSlew(t *testing.T) {
	store, _ := testStore(t)
	withSessionClock(t, func(set func(time.Time)) {
		set(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		record, err := store.Create(testCreateInput(RolePlanner))
		if err != nil {
			t.Fatal(err)
		}
		created := record.CreatedAt

		// Clock steps backward across every mutation path; each write must
		// clamp to the persisted timeline instead of recording a regressed
		// wall-clock sample.
		set(created.Add(-30 * time.Second))
		label := "renamed"
		updated, err := store.Update(record.ID, UpdateInput{Label: &label})
		if err != nil {
			t.Fatalf("update under regressed clock: %v", err)
		}
		if updated.UpdatedAt.Before(created) {
			t.Fatalf("update regressed UpdatedAt below CreatedAt: %#v", updated)
		}

		set(created.Add(-45 * time.Second))
		acked, err := store.AcknowledgeRules(record.ID, "r1", "digest-g", "digest-p")
		if err != nil {
			t.Fatalf("acknowledge under regressed clock: %v", err)
		}
		if acked.UpdatedAt.Before(created) {
			t.Fatalf("acknowledge regressed UpdatedAt below CreatedAt: %#v", acked)
		}

		set(created.Add(-60 * time.Second))
		ended, err := store.End(record.ID)
		if err != nil {
			t.Fatalf("end under regressed clock: %v", err)
		}
		if ended.EndedAt == nil || ended.EndedAt.Before(ended.StartedAt) || ended.UpdatedAt.Before(created) {
			t.Fatalf("end regressed durable ordering under slew: %#v", ended)
		}
	})
}

// TestTSK672SessionTimestampsStayMonotonicAcrossRestart proves the clamp
// travels with the persisted record: a regressed clock on a fresh Store
// instance (a restart or second process) still yields nondecreasing writes.
func TestTSK672SessionTimestampsStayMonotonicAcrossRestart(t *testing.T) {
	state := t.TempDir()
	var record Record
	withSessionClock(t, func(set func(time.Time)) {
		set(time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC))
		db, err := sqlitestore.Open(state)
		if err != nil {
			t.Fatal(err)
		}
		store := NewStoreWithDurability(db)
		created, err := store.Create(testCreateInput(RolePlanner))
		if err != nil {
			t.Fatal(err)
		}
		record = created
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})

	// Reopen the durable store on a new Store instance — a separate process.
	db, err := sqlitestore.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	restarted := NewStoreWithDurability(db)
	withSessionClock(t, func(set func(time.Time)) {
		set(record.CreatedAt.Add(-90 * time.Second))
		label := "after-restart"
		updated, err := restarted.Update(record.ID, UpdateInput{Label: &label})
		if err != nil {
			t.Fatalf("update after restart under regressed clock: %v", err)
		}
		if updated.UpdatedAt.Before(record.UpdatedAt) {
			t.Fatalf("restart allowed UpdatedAt regression: %#v", updated)
		}
	})
}

// TestTSK672CorruptTimestampsRemainStrict ensures the clamp does not mask
// genuinely corrupt persisted state: a persisted violation still fails
// validation on read.
func TestTSK672CorruptTimestampsRemainStrict(t *testing.T) {
	record := Record{
		SchemaVersion: SchemaVersion,
		ID:            "HOM_EXM_P_abcde",
		ProjectID:     "example",
		ProjectCode:   "EXM",
		Role:          RolePlanner,
		SessionType:   SessionTypeChatGPT,
		Status:        StatusActive,
		CreatedAt:     time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		StartedAt:     time.Date(2026, 10, 1, 11, 59, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 10, 1, 11, 58, 0, 0, time.UTC),
	}
	ref := "binding"
	record.SessionRef = &ref
	if err := record.Validate(); err == nil || !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("corrupt timestamp ordering passed validation: %v", err)
	}
}
