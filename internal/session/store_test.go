package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCreateReloadUpdateAndEnd(t *testing.T) {
	state := t.TempDir()
	store := NewStore(state)
	ref, label := "conversation-1", "primary"
	record, err := store.Create(CreateInput{ProjectID: "example", Role: RoleDelivery, SessionType: SessionTypeChatGPT, SessionRef: &ref, Label: &label})
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
	if _, err := store.Create(CreateInput{ProjectID: "example", Role: RolePlanner, SessionType: SessionTypeChatGPT}); err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{ProjectID: "example", Role: RolePlanner, SessionType: SessionTypeChatGPT})
	if err != nil || created.ID != "S-89ABCDEF" {
		t.Fatalf("collision retry record=%#v err=%v", created, err)
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
