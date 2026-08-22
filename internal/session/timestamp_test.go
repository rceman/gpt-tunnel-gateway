package session

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

func TestMonotonicSessionTimestampClampsClockRollback(t *testing.T) {
	previous := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	if got := monotonicSessionTimestamp(previous, previous.Add(-time.Second)); !got.Equal(previous) {
		t.Fatalf("rollback timestamp=%s, want %s", got, previous)
	}
}

func TestSessionUpdatePreservesPersistedTimestampAcrossClockRollback(t *testing.T) {
	store := NewStore(t.TempDir())
	record, err := store.Create(CreateInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		Role:        RoleAgent,
		SessionType: SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(time.Hour)
	record.UpdatedAt = future
	if err := fsutil.WriteJSONAtomic(store.path(record.ID), record, 0o600); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Update(record.ID, UpdateInput{Label: stringPtr("after rollback")})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.UpdatedAt.Equal(future) {
		t.Fatalf("updated_at=%s, want persisted timestamp %s", updated.UpdatedAt, future)
	}
}

func stringPtr(value string) *string { return &value }
