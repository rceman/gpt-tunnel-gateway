package sqlitestore

import (
	"context"
	"testing"
	"time"
)

func TestTSK580TokenUsageIsAtomicDeduplicatedAndBodyFree(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	event := TokenUsageEvent{
		EventID:      "event-1",
		SessionID:    "SA-TEST",
		InputTokens:  3,
		OutputTokens: 5,
		TotalTokens:  8,
		RecordedAt:   now,
	}
	if err := db.RecordTokenUsage(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordTokenUsage(ctx, event); err != nil {
		t.Fatal(err)
	}
	event.EventID = "event-2"
	if err := db.RecordTokenUsage(ctx, event); err != nil {
		t.Fatal(err)
	}
	usage, err := db.ReadTokenUsage(ctx, event.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.RequestCount != 2 || usage.InputTokens != 6 || usage.OutputTokens != 10 || usage.TotalTokens != 16 {
		t.Fatalf("usage=%#v", usage)
	}
	columns, err := db.Local.Query(ctx, `SELECT name FROM pragma_table_info('local_token_usage_events') WHERE name LIKE '%body%' OR name LIKE '%payload%'`)
	if err != nil || len(columns.Rows) != 0 {
		t.Fatalf("body-like columns=%#v err=%v", columns.Rows, err)
	}
	if _, err := db.Local.Exec(ctx, `DROP TABLE local_token_usage`); err != nil {
		t.Fatal(err)
	}
	event.EventID = "event-3"
	if err := db.RecordTokenUsage(ctx, event); err == nil {
		t.Fatal("usage write unexpectedly succeeded with missing aggregate table")
	}
	rows, err := db.Local.Query(ctx, `SELECT COUNT(*) FROM local_token_usage_events WHERE event_id='event-3'`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(0) {
		t.Fatalf("atomic rollback rows=%#v err=%v", rows.Rows, err)
	}
}
