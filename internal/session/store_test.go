package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func testStore(t *testing.T) (Store, string) {
	t.Helper()
	state := t.TempDir()
	db, err := sqlitestore.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStoreWithDurability(db), state
}

func testCreateInput(role string) CreateInput {
	return CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: role, SessionType: SessionTypeChatGPT}
}

func TestStoreSQLiteLifecycleHasNoSessionJSONAuthority(t *testing.T) {
	store, state := testStore(t)
	ref, label := "conversation-1", "primary"
	record, err := store.Create(CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: RoleAgent, SessionType: SessionTypeChatGPT, SessionRef: &ref, Label: &label})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(record.ID)
	if err != nil || got.ID != record.ID {
		t.Fatalf("get=%#v err=%v", got, err)
	}
	updatedLabel := "renamed"
	updated, err := store.Update(record.ID, UpdateInput{Label: &updatedLabel})
	if err != nil || *updated.Label != updatedLabel {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	ended, err := store.End(record.ID)
	if err != nil || ended.Status != StatusEnded {
		t.Fatalf("end=%#v err=%v", ended, err)
	}
	if _, err := store.Update(record.ID, UpdateInput{Label: &updatedLabel}); !errors.Is(err, ErrAlreadyEnded) {
		t.Fatalf("ended update=%v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "sessions", record.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("session JSON authority exists: %v", err)
	}
}

func TestStoreConcurrentCreateUsesOneLocalDBAndUniqueIDs(t *testing.T) {
	store, _ := testStore(t)
	const count = 32
	ids := make(chan string, count)
	errs := make(chan error, count)
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			record, err := store.CreateUnbound(RolePlanner, nil)
			if err != nil {
				errs <- err
				return
			}
			ids <- record.ID
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("IDs=%d want=%d", len(seen), count)
	}
}

func TestStoreCreateCollisionRegeneratesWithoutOverwrite(t *testing.T) {
	store, _ := testStore(t)
	ids := []string{"SP-EXM-0123", "SP-EXM-0123", "SP-EXM-89AB"}
	store.IDGenerator = func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
	if _, err := store.Create(testCreateInput(RolePlanner)); err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(testCreateInput(RolePlanner))
	if err != nil || created.ID != "SP-EXM-89AB" {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	if got, err := store.Get("SP-EXM-0123"); err != nil || got.ID != "SP-EXM-0123" {
		t.Fatalf("collision row=%#v err=%v", got, err)
	}
}

func TestStoreBindAppliesProjectAndRefInOneRecordMutation(t *testing.T) {
	store, _ := testStore(t)
	record, err := store.CreateUnbound(RolePlanner, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "planner-ref"
	bound, err := store.Bind(record.ID, "example", &ref)
	if err != nil {
		t.Fatal(err)
	}
	if bound.ProjectID != "example" || bound.SessionRef == nil || *bound.SessionRef != ref {
		t.Fatalf("bound=%#v", bound)
	}
}

func TestStoreConcurrentEndIsIdempotent(t *testing.T) {
	store, _ := testStore(t)
	record, err := store.CreateUnbound(RolePlanner, nil)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() { defer group.Done(); _, err := store.End(record.ID); results <- err }()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Get(record.ID)
	if err != nil || got.Status != StatusEnded {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestCutoverValidatesThenBatchImportsAndCleansAllFiles(t *testing.T) {
	store, state := testStore(t)
	now := time.Now().UTC()
	records := []Record{{SchemaVersion: SchemaVersion, ID: "SP-ABC12345", Role: RolePlanner, SessionType: SessionTypeChatGPT, Status: StatusActive, CreatedAt: now, StartedAt: now, UpdatedAt: now}, {SchemaVersion: SchemaVersion, ID: "SA-ABC12345", Role: RoleAgent, SessionType: SessionTypeChatGPT, Status: StatusActive, CreatedAt: now, StartedAt: now, UpdatedAt: now}}
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		raw, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(dir, record.ID+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := CutoverLegacyJSON(nil, state, store.Durability); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if _, err := os.Stat(filepath.Join(dir, record.ID+".json")); !os.IsNotExist(err) {
			t.Fatalf("legacy file remains: %v", err)
		}
		if got, err := store.Get(record.ID); err != nil || got.ID != record.ID {
			t.Fatalf("import=%#v err=%v", got, err)
		}
	}
}

func TestCutoverRejectsUnexpectedOrConflictingInputBeforeInsert(t *testing.T) {
	store, state := testStore(t)
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CutoverLegacyJSON(nil, state, store.Durability); err == nil {
		t.Fatal("unexpected legacy entry accepted")
	}
}
