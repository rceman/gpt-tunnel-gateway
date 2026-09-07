package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestCutoverConflictInsertsNothingAndPreservesEvidence(t *testing.T) {
	store, state := testStore(t)
	now := time.Now().UTC()
	missing := Record{
		SchemaVersion: SchemaVersion,
		ID:            "SP-ABC12345",
		Role:          RolePlanner,
		SessionType:   SessionTypeChatGPT,
		Status:        StatusActive,
		CreatedAt:     now,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	conflict := Record{
		SchemaVersion: SchemaVersion,
		ID:            "SA-ABC12345",
		Role:          RoleAgent,
		SessionType:   SessionTypeChatGPT,
		Status:        StatusActive,
		CreatedAt:     now,
		StartedAt:     now,
		UpdatedAt:     now,
		Label:         stringPtr("legacy"),
	}
	rawMissing, _ := json.Marshal(missing)
	rawConflict, _ := json.Marshal(conflict)
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, missing.ID+".json"), rawMissing, 0o600); err != nil {
		t.Fatal(err)
	}
	conflictPath := filepath.Join(dir, conflict.ID+".json")
	if err := os.WriteFile(conflictPath, rawConflict, 0o600); err != nil {
		t.Fatal(err)
	}
	altered := conflict
	altered.Label = stringPtr("database")
	altered.Status = StatusEnded
	endedAt := now.Add(time.Second)
	altered.EndedAt = &endedAt
	altered.UpdatedAt = now.Add(2 * time.Second)
	alteredRaw, _ := json.Marshal(altered)
	if err := store.Durability.CreateLocalSession(context.Background(), sqlitestore.LocalSession{ID: conflict.ID, Payload: alteredRaw, UpdatedAt: altered.UpdatedAt.Format(time.RFC3339Nano), Status: altered.Status}); err != nil {
		t.Fatal(err)
	}
	if err := CutoverLegacyJSON(context.Background(), state, store.Durability); err == nil {
		t.Fatal("conflict accepted")
	}
	if _, err := store.Get(missing.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial import occurred: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, missing.ID+".json")); err != nil {
		t.Fatalf("missing-row evidence missing: %v", err)
	}
	if _, err := os.Stat(conflictPath); err != nil {
		t.Fatalf("conflict evidence missing: %v", err)
	}
	got, err := store.Durability.ReadLocalSession(context.Background(), conflict.ID)
	if err != nil || string(got.Payload) != string(alteredRaw) || got.Status != altered.Status || got.UpdatedAt != altered.UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("preexisting row changed: %#v err=%v", got, err)
	}
}

func stringPtr(value string) *string { return &value }

func TestCutoverRejectsUnexpectedOrConflictingInputBeforeInsert(t *testing.T) {
	store, state := testStore(t)
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CutoverLegacyJSON(context.Background(), state, store.Durability); err == nil {
		t.Fatal("unexpected legacy entry accepted")
	}
}
