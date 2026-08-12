package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCreateReloadUpdateAndEnd(t *testing.T) {
	state := t.TempDir()
	store := NewStore(state)
	ref, label := "conversation-1", "primary"
	record, err := store.Create(CreateInput{
		ProjectID:   "example",
		Role:        RoleDelivery,
		SessionType: SessionTypeChatGPT,
		SessionRef:  &ref,
		Label:       &label,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sessionIDRE.MatchString(record.ID) || record.Status != StatusActive || record.EndedAt != nil {
		t.Fatalf("created record=%#v", record)
	}
	info, err := NewStore(state).Get(record.ID)
	if err != nil || info.ID != record.ID || info.ProjectID != "example" || info.Role != RoleDelivery || info.SessionRef == nil || *info.SessionRef != ref {
		t.Fatalf("reloaded record=%#v err=%v", info, err)
	}
	updatedLabel := "renamed"
	updated, err := store.Update(record.ID, UpdateInput{Label: &updatedLabel})
	if err != nil || updated.SessionRef == nil || *updated.SessionRef != ref || updated.Label == nil || *updated.Label != updatedLabel {
		t.Fatalf("updated record=%#v err=%v", updated, err)
	}
	ended, err := store.End(record.ID)
	if err != nil || ended.Status != StatusEnded || ended.EndedAt == nil {
		t.Fatalf("ended record=%#v err=%v", ended, err)
	}
	if _, err := store.Update(record.ID, UpdateInput{Label: &updatedLabel}); !errors.Is(err, ErrAlreadyEnded) {
		t.Fatalf("ended session update error=%v", err)
	}
	info, err = store.Get(record.ID)
	if err != nil || info.Status != StatusEnded {
		t.Fatalf("persisted ended record=%#v err=%v", info, err)
	}
	mode, err := os.Stat(filepath.Join(state, "sessions", record.ID+".json"))
	if err != nil || mode.Mode().Perm() != 0o600 {
		t.Fatalf("session mode=%v err=%v", mode.Mode(), err)
	}
}

func TestStoreRetriesIDCollisionAtomically(t *testing.T) {
	state := t.TempDir()
	ids := []string{"S-01234567", "S-01234567", "S-89ABCDEF"}
	store := NewStore(state)
	store.IDGenerator = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	if _, err := store.Create(CreateInput{
		ProjectID:   "example",
		Role:        RolePlanner,
		SessionType: SessionTypeChatGPT,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{
		ProjectID:   "example",
		Role:        RolePlanner,
		SessionType: SessionTypeChatGPT,
	})
	if err != nil || created.ID != "S-89ABCDEF" {
		t.Fatalf("collision retry record=%#v err=%v", created, err)
	}
}

func TestStoreCreatesRoleTypedIDsAndReadsLegacyIDs(t *testing.T) {
	state := t.TempDir()
	store := NewStore(state)
	for role, prefix := range map[string]string{RolePlanner: "SP-", RoleDelivery: "SD-", RoleAgent: "SA-", RoleWatcher: "SW-"} {
		record, err := store.Create(CreateInput{
			ProjectID:   "example",
			Role:        role,
			SessionType: SessionTypeChatGPT,
		})
		if err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		if len(record.ID) != 11 || record.ID[:3] != prefix {
			t.Fatalf("role %s received ID %q", role, record.ID)
		}
	}

	now := time.Now().UTC()
	legacy := Record{
		SchemaVersion: SchemaVersion,
		ID:            "S-ABC12345",
		ProjectID:     "example",
		Role:          RolePlanner,
		SessionType:   SessionTypeChatGPT,
		Status:        StatusActive,
		CreatedAt:     now,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "sessions", legacy.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(legacy.ID); err != nil || got.ID != legacy.ID {
		t.Fatalf("legacy session was not readable: %#v %v", got, err)
	}
}

func TestStoreRejectsTypedRolePrefixMismatch(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		role string
		id   string
	}{
		{RolePlanner, "SD-ABC12345"},
		{RoleDelivery, "SP-ABC12345"},
		{RoleAgent, "SW-ABC12345"},
		{RoleWatcher, "SA-ABC12345"},
	} {
		record := Record{
			SchemaVersion: SchemaVersion,
			ID:            test.id,
			ProjectID:     "example",
			Role:          test.role,
			SessionType:   SessionTypeChatGPT,
			Status:        StatusActive,
			CreatedAt:     now,
			StartedAt:     now,
			UpdatedAt:     now,
		}
		if err := record.Validate(); err == nil {
			t.Fatalf("typed ID %q accepted for mismatched role %q", test.id, test.role)
		}
	}
}

func TestStoreRejectsInvalidBindingAndCorruptFiles(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, input := range []CreateInput{
		{ProjectID: "example", Role: "operator", SessionType: SessionTypeChatGPT},
		{ProjectID: "example", Role: RoleDelivery, SessionType: "unknown"},
	} {
		if _, err := store.Create(input); err == nil {
			t.Fatalf("invalid input accepted: %#v", input)
		}
	}
	if _, err := store.Get("S-00000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session error=%v", err)
	}
}
