package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTSK578LegacyIDsAreRejectedAndNotTranslated(t *testing.T) {
	store, state := testStore(t)
	legacyIDs := []string{"SP-ABC12345", "SA-ABC12345", "S-ABC12345"}
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range legacyIDs {
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"session_id":"`+id+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(id); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("Get(%q) err=%v", id, err)
		}
	}
	if err := CutoverLegacyJSON(context.Background(), state, store.Durability); err != nil {
		t.Fatal(err)
	}
	records, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("legacy records were translated: %#v", records)
	}
	for _, id := range legacyIDs {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
			t.Fatalf("legacy evidence %q changed: %v", id, err)
		}
	}
}
