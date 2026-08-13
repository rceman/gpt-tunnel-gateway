package runtime_log

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEvent(n string) Event {
	return Event{
		Timestamp:   time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		Level:       "info",
		Component:   "gateway",
		Event:       n,
		OperationID: "op-1",
		Message:     "safe",
	}
}

func TestStoreAppendReadFiltersAndRedacts(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := store.Append(Event{
		Timestamp: time.Now().UTC(),
		Level:     "info",
		Component: "mcp",
		Event:     "action_finish",
		Message:   "authorization=hidden",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := store.Read(Filter{
		Component: "mcp",
		Limit:     1,
	})
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("read result=%+v err=%v", result, err)
	}
	if strings.Contains(result.Events[0].Message, "hidden") {
		t.Fatal("runtime log exposed credential text")
	}
}

func TestStoreRotatesAndToleratesMalformedLines(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	store.MaxBytes = 180
	for _, name := range []string{"one", "two", "three"} {
		if err := store.Append(testEvent(name)); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "runtime", "events.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("not-json\n")
	_ = file.Close()
	result, err := store.Read(Filter{Limit: MaxLimit})
	if err != nil || result.MalformedLines != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotation file missing: %v", err)
	}
}

func TestEventJSONIsBoundedAndStrict(t *testing.T) {
	event := testEvent("action_start")
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) > 64<<10 {
		t.Fatalf("encoded event err=%v len=%d", err, len(encoded))
	}
	if err := (Event{
		Timestamp: time.Now().UTC(),
		Level:     "info",
		Component: "gateway",
		Event:     "bad\nline",
	}).Validate(); err == nil {
		t.Fatal("expected invalid event identity")
	}
}
