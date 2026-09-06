package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk514LegacyRecord(id, role string, now time.Time) Record {
	return Record{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Role:          role,
		SessionType:   SessionTypeChatGPT,
		Status:        StatusActive,
		CreatedAt:     now,
		StartedAt:     now,
		UpdatedAt:     now,
	}
}

func tsk514WriteLegacy(t *testing.T, state string, records ...Record) {
	t.Helper()
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, record.ID+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTSK514RecordValidationKeepsPlannerAndAgentPolicyHardCut(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		record Record
		valid  bool
	}{
		{name: "planner", record: tsk514LegacyRecord("SP-GTW-120E", RolePlanner, now), valid: true},
		{name: "agent", record: tsk514LegacyRecord("SA-GTW-BEYB", RoleAgent, now), valid: true},
		{name: "delivery retired", record: tsk514LegacyRecord("S-ABC12345", "delivery", now)},
		{name: "watcher rejected", record: tsk514LegacyRecord("SW-ABC12345", "watcher", now)},
		{name: "other rejected", record: tsk514LegacyRecord("S-ABC12345", "other", now)},
		{name: "planner wrong prefix", record: tsk514LegacyRecord("SA-ABC12345", RolePlanner, now)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.record.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate()=%v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("Validate unexpectedly accepted retired/invalid role")
			}
		})
	}
}

func TestTSK514CutoverMixedRolesImportsOnlyCurrentAndCleansAllValidatedFiles(t *testing.T) {
	store, state := testStore(t)
	now := time.Now().UTC()
	planner := tsk514LegacyRecord("SP-GTW-120E", RolePlanner, now)
	planner.ProjectID, planner.ProjectCode = "gpt-tunnel-gateway", "GTW"
	agent := tsk514LegacyRecord("SA-GTW-BEYB", RoleAgent, now)
	agent.ProjectID, agent.ProjectCode = "gpt-tunnel-gateway", "GTW"
	deliveryS := tsk514LegacyRecord("S-ABC12345", "delivery", now)
	deliverySD := tsk514LegacyRecord("SD-ABC12345", "delivery", now)
	tsk514WriteLegacy(t, state, planner, agent, deliveryS, deliverySD)

	if err := CutoverLegacyJSON(context.Background(), state, store.Durability); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{planner.ID, agent.ID} {
		if _, err := store.Get(id); err != nil {
			t.Fatalf("current session %s was not imported: %v", id, err)
		}
	}
	for _, id := range []string{deliveryS.ID, deliverySD.ID} {
		if _, err := store.Get(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("retired delivery %s imported: %v", id, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(state, "sessions"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("validated legacy files remain: %d", len(entries))
	}
}

func TestTSK514CutoverRejectsMalformedRetiredAndArbitraryRolesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		record Record
	}{
		{name: "malformed delivery", record: func() Record {
			r := tsk514LegacyRecord("S-ABC12345", "delivery", time.Now().UTC())
			r.UpdatedAt = time.Time{}
			return r
		}()},
		{name: "watcher", record: tsk514LegacyRecord("SW-ABC12345", "watcher", time.Now().UTC())},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, state := testStore(t)
			valid := tsk514LegacyRecord("SP-GTW-120E", RolePlanner, time.Now().UTC())
			tsk514WriteLegacy(t, state, valid, test.record)
			if err := CutoverLegacyJSON(context.Background(), state, store.Durability); err == nil {
				t.Fatal("invalid legacy record accepted")
			}
			if _, err := store.Get(valid.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("partial import occurred: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(state, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 {
				t.Fatalf("failure cleaned evidence: %d files remain", len(entries))
			}
		})
	}
}

func TestTSK514CutoverRestartAfterCommittedRowsCleansDeliveryFiles(t *testing.T) {
	store, state := testStore(t)
	now := time.Now().UTC()
	planner := tsk514LegacyRecord("SP-GTW-120E", RolePlanner, now)
	agent := tsk514LegacyRecord("SA-GTW-BEYB", RoleAgent, now)
	tsk514WriteLegacy(t, state, planner, agent, tsk514LegacyRecord("S-ABC12345", "delivery", now))
	for _, record := range []Record{planner, agent} {
		raw, _ := json.Marshal(record)
		if err := store.Durability.CreateLocalSession(context.Background(), sqlitestore.LocalSession{ID: record.ID, Payload: raw, UpdatedAt: record.UpdatedAt.Format(time.RFC3339Nano), Status: record.Status}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := CutoverLegacyJSON(context.Background(), state, store.Durability); err != nil {
			t.Fatalf("restart pass %d: %v", i+1, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(state, "sessions"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("restart did not clean validated files: %d", len(entries))
	}
}

func TestTSK514RealLegacySnapshotCutover(t *testing.T) {
	source := os.Getenv("GTW_TSK514_REAL_STATE_DIR")
	if source == "" {
		t.Skip("GTW_TSK514_REAL_STATE_DIR not set")
	}
	sourceEntries, err := os.ReadDir(filepath.Join(source, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceEntries) != 70 {
		t.Fatalf("source legacy file count=%d, want 70", len(sourceEntries))
	}
	state := t.TempDir()
	dest := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	var expected []string
	for _, entry := range sourceEntries {
		if filepath.Ext(entry.Name()) != ".json" {
			t.Fatalf("unexpected source entry %q", entry.Name())
		}
		raw, err := os.ReadFile(filepath.Join(source, "sessions", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var record Record
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.Role == RolePlanner || record.Role == RoleAgent {
			expected = append(expected, record.ID)
		}
		if err := os.WriteFile(filepath.Join(dest, entry.Name()), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlitestore.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := CutoverLegacyJSON(context.Background(), state, db); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListLocalSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.ID)
	}
	sort.Strings(expected)
	sort.Strings(got)
	if len(got) != len(expected) || len(got) != 48 {
		t.Fatalf("imported session count=%d, want 48", len(got))
	}
	if len(got) > 0 && (got[0] == "" || !containsString(got, "SP-GTW-120E") || !containsString(got, "SA-GTW-BEYB")) {
		t.Fatalf("snapshot imported IDs omit required records")
	}
	if len(rows) == 0 || containsPrefix(got, "S-") || containsPrefix(got, "SD-") {
		t.Fatalf("snapshot imported retired delivery record")
	}
	entries, err := os.ReadDir(dest)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("snapshot legacy files remain: %d", len(entries))
	}
	t.Logf("snapshot imported current sessions=%d delivery=0 required=%s,%s", len(got), "SP-GTW-120E", "SA-GTW-BEYB")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
