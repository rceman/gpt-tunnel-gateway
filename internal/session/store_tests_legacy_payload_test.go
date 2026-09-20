package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestStoreUpdateUsesStoredPayloadForLegacyRecordShape(t *testing.T) {
	store, _ := testStore(t)
	record, err := store.Create(testCreateInput(RolePlanner))
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.Durability.ReadLocalSession(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(row.Payload, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["project_rules_revision"] = "legacy-revision"
	legacyPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Durability.Local.Exec(context.Background(), `UPDATE local_sessions SET payload=? WHERE session_id=?`, legacyPayload, record.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	label := "repaired"
	updated, err := store.Update(record.ID, UpdateInput{Label: &label})
	if err != nil {
		t.Fatalf("legacy-shaped session update failed: %v", err)
	}
	if updated.Label == nil || *updated.Label != label {
		t.Fatalf("updated session=%#v", updated)
	}
	stored, err := store.Durability.ReadLocalSession(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stored.Payload, loaded.rawPayload) {
		t.Fatal("successful update retained the legacy payload")
	}
}

func TestStoreUpdateRetainsPayloadCASForStaleReads(t *testing.T) {
	store, _ := testStore(t)
	record, err := store.Create(testCreateInput(RolePlanner))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstLabel := "first"
	firstNext := first
	firstNext.Label = &firstLabel
	firstNext.UpdatedAt = time.Now().UTC()
	if err := firstNext.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := store.updateLocal(first, firstNext); err != nil {
		t.Fatal(err)
	}
	secondLabel := "second"
	secondNext := second
	secondNext.Label = &secondLabel
	secondNext.UpdatedAt = time.Now().UTC()
	if err := secondNext.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := store.updateLocal(second, secondNext); !errors.Is(err, sqlitestore.ErrLocalSessionChanged) {
		t.Fatalf("stale update error=%v, want ErrLocalSessionChanged", err)
	}
}
