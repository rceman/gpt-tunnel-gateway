package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTSK578LegacyCutoverDoesNotImportMalformedFiles(t *testing.T) {
	store, state := testStore(t)
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SA-ABC12345.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CutoverLegacyJSON(context.Background(), state, store.Durability); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("SA-ABC12345"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("malformed legacy ID err=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("malformed legacy evidence changed: %v", err)
	}
}
