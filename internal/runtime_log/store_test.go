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
	if len(result.Events) == 0 || result.Events[0].Event != "three" {
		t.Fatalf("newest rotated event was not first: %#v", result.Events)
	}
}

func TestStoreNewestWindowFiltersAndContinuation(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	base := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{Timestamp: base, Level: "info", Component: "gateway", Event: "process_ready", Action: "gateway/start", RequestID: "req-old", SessionID: "session-a", ProjectID: "example"},
		{Timestamp: base.Add(time.Minute), Level: "warn", Component: "gateway", Event: "action_failure", Action: "train/add", RequestID: "req-one", SessionID: "session-a", ProjectID: "example"},
		{Timestamp: base.Add(2 * time.Minute), Level: "warn", Component: "gateway", Event: "action_failure", Action: "train/add", RequestID: "req-two", SessionID: "session-a", ProjectID: "example"},
		{Timestamp: base.Add(3 * time.Minute), Level: "warn", Component: "gateway", Event: "action_failure", Action: "train/add", RequestID: "req-three", SessionID: "session-a", ProjectID: "example"},
		{Timestamp: base.Add(4 * time.Minute), Level: "warn", Component: "gateway", Event: "action_failure", Action: "other/action", RequestID: "req-four", SessionID: "session-b", ProjectID: "other"},
	}
	for _, event := range events {
		if err := store.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.Read(Filter{
		Limit:     2,
		Action:    "train/add",
		RequestID: "req-three",
		SessionID: "session-a",
		ProjectID: "example",
	})
	if err != nil || len(first.Events) != 1 || first.Events[0].RequestID != "req-three" || first.HasMore {
		t.Fatalf("combined newest filter=%#v err=%v", first, err)
	}
	window, err := store.Read(Filter{
		Limit:     2,
		Action:    "train/add",
		SessionID: "session-a",
		ProjectID: "example",
	})
	if err != nil || len(window.Events) != 2 || window.Events[0].RequestID != "req-three" || window.Events[1].RequestID != "req-two" || !window.HasMore || window.NextCursor == "" {
		t.Fatalf("newest window=%#v err=%v", window, err)
	}
	continued, err := store.Read(Filter{
		Limit:     2,
		Action:    "train/add",
		SessionID: "session-a",
		ProjectID: "example",
		Cursor:    window.NextCursor,
	})
	if err != nil || len(continued.Events) != 1 || continued.Events[0].RequestID != "req-one" || continued.HasMore {
		t.Fatalf("continuation=%#v err=%v", continued, err)
	}
	missing, err := store.Read(Filter{
		Limit:     5,
		Action:    "train/add",
		RequestID: "missing",
	})
	if err != nil || len(missing.Events) != 0 || missing.HasMore || missing.NextCursor != "" {
		t.Fatalf("no-match result=%#v err=%v", missing, err)
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
