package sqlitestore

import (
	"context"
	"testing"
)

func TestMilestoneSharedLifecycleDescriptorAndSchema(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := SharedLifecycleStatusValues("milestone", false); len(got) != 4 || got[0] != "planned" || got[1] != "active" || got[2] != "completed" || got[3] != "archived" {
		t.Fatalf("milestone statuses=%v", got)
	}
	rows, err := db.Shared.Query(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name='shared_milestones'`)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("shared milestone table rows=%#v err=%v", rows.Rows, err)
	}
	code, next, found, err := db.ReadSharedSequence(context.Background(), "milestone", "example")
	if err != nil {
		t.Fatal(err)
	}
	if found || code != "" || next != 0 {
		t.Fatalf("unexpected unallocated milestone sequence: %q %d %v", code, next, found)
	}
}
