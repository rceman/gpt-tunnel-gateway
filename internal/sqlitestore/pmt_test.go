package sqlitestore

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func testPMT() model.PMT {
	return model.PMT{
		SchemaVersion: model.PMTSchemaVersion, ProjectID: "example", ProjectCode: "EXM",
		Title: "bounded title", Instruction: "read the durable prompt", PlannerSessionID: "SP-ABCDEFGH",
		TargetSessionID: "SA-ABCDEFGH", TargetAirelaySessionKey: "example_master", TargetAgentID: "coding",
		CreatedAt: time.Now().UTC(), State: model.PMTStateUnread, Reference: "pending",
	}
}

func TestPMTLocalLifecycleAndAtomicCancel(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	created, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "EXM-PMT1" {
		t.Fatalf("id=%q", created.ID)
	}
	read, err := db.ReadPMT(ctx, created.ID)
	if err != nil || read.Instruction != "read the durable prompt" {
		t.Fatalf("read=%#v err=%v", read, err)
	}
	queue, count, err := db.ListPendingPMTs(ctx, "example", "SA-ABCDEFGH", 8)
	if err != nil || count != 1 || len(queue) != 1 {
		t.Fatalf("queue=%#v count=%d err=%v", queue, count, err)
	}
	fetched, changed, err := db.MarkPMTFetched(ctx, created.ID, time.Now().UTC())
	if err != nil || !changed || fetched.State != model.PMTStateFetched {
		t.Fatalf("fetch=%#v changed=%v err=%v", fetched, changed, err)
	}
	again, changed, err := db.MarkPMTFetched(ctx, created.ID, time.Now().UTC())
	if err != nil || !changed || again.ReadCount != fetched.ReadCount+1 {
		t.Fatalf("repeat=%#v changed=%v err=%v", again, changed, err)
	}
	cancelTarget, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := db.CancelPMT(ctx, cancelTarget.ID, time.Now().UTC())
	if err != nil || !cancelled {
		t.Fatalf("cancelled=%v err=%v", cancelled, err)
	}
	_, changed, err = db.MarkPMTFetched(ctx, cancelTarget.ID, time.Now().UTC())
	if err != nil || changed {
		t.Fatalf("cancel/fetch race changed=%v err=%v", changed, err)
	}
	fetchedTarget, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := db.MarkPMTFetched(ctx, fetchedTarget.ID, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("fetch before cancel changed=%v err=%v", changed, err)
	}
	if cancelled, err := db.CancelPMT(ctx, fetchedTarget.ID, time.Now().UTC()); err == nil || cancelled {
		t.Fatalf("fetched PMT cancel cancelled=%v err=%v", cancelled, err)
	}
}

func TestPMTSupersedeIsAtomicAndUsesLocalSequence(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	old, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	replacement := testPMT()
	replacement.Title = "replacement"
	newPMT, err := db.SupersedeAndCreatePMT(ctx, []string{old.ID}, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if newPMT.ID != "EXM-PMT2" {
		t.Fatalf("replacement id=%q", newPMT.ID)
	}
	oldRead, err := db.ReadPMT(ctx, old.ID)
	if err != nil || oldRead.State != model.PMTStateSuperseded || oldRead.SupersededBy != newPMT.ID {
		t.Fatalf("old=%#v err=%v", oldRead, err)
	}
	newRead, err := db.ReadPMT(ctx, newPMT.ID)
	if err != nil || newRead.Instruction != replacement.Instruction {
		t.Fatalf("new=%#v err=%v", newRead, err)
	}
}

func TestPMTQueueCancelAndExpiryLifecycle(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	one, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	two, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	three, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	queue, count, err := db.ListPendingPMTs(ctx, "example", "SA-ABCDEFGH", 8)
	if err != nil || count != 3 || len(queue) != 3 {
		t.Fatalf("initial queue=%#v count=%d err=%v", queue, count, err)
	}
	cancelled, err := db.CancelPMT(ctx, two.ID, time.Now().UTC())
	if err != nil || !cancelled {
		t.Fatalf("cancelled=%v err=%v", cancelled, err)
	}
	queue, count, err = db.ListPendingPMTs(ctx, "example", "SA-ABCDEFGH", 8)
	if err != nil || count != 2 || len(queue) != 2 || queue[0].ID != one.ID || queue[1].ID != three.ID {
		t.Fatalf("refreshed queue=%#v count=%d err=%v", queue, count, err)
	}

	expires := time.Now().UTC().Add(-time.Minute)
	expired := testPMT()
	expired.ExpiresAt = &expires
	expired, err = db.CreatePMT(ctx, expired)
	if err != nil {
		t.Fatal(err)
	}
	fetched, changed, err := db.MarkPMTFetched(ctx, expired.ID, time.Now().UTC())
	if err != nil || changed || fetched.State != model.PMTStateExpired {
		t.Fatalf("expired fetch=%#v changed=%v err=%v", fetched, changed, err)
	}
	if err := db.PrunePMTs(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReadPMT(ctx, expired.ID); err == nil {
		t.Fatal("expired PMT was not pruned")
	}
}

func TestPMTUnreadSurvivesPendingGC(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	pmt, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Local.Exec(ctx, `UPDATE local_pmts SET created_at=? WHERE id=?`, time.Now().UTC().Add(-48*time.Hour).Format(time.RFC3339Nano), pmt.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.PrunePMTs(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReadPMT(ctx, pmt.ID); err != nil {
		t.Fatalf("pending PMT was collected: %v", err)
	}
}

func TestPMTQueueIsScopedByTargetSessionID(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	first := testPMT()
	first.TargetSessionID = "SA-ABCDEFGH"
	second := testPMT()
	second.TargetSessionID = "SA-IJKLMNOP"
	if _, err := db.CreatePMT(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePMT(ctx, second); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"SA-ABCDEFGH", "SA-IJKLMNOP"} {
		queue, count, err := db.ListPendingPMTs(ctx, "example", target, 8)
		if err != nil || count != 1 || len(queue) != 1 {
			t.Fatalf("target=%q queue=%#v count=%d err=%v", target, queue, count, err)
		}
	}
}

func TestPMTStatePersistsAcrossRestart(t *testing.T) {
	stateDir := t.TempDir()
	ctx := context.Background()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	pmt, err := db.CreatePMT(ctx, testPMT())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, _, err := db.MarkPMTFetched(ctx, pmt.ID, time.Now().UTC()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	read, err := db.ReadPMT(ctx, pmt.ID)
	if err != nil || read.State != model.PMTStateFetched || read.Instruction != pmt.Instruction || read.ReadCount != 1 {
		t.Fatalf("restart read=%#v err=%v", read, err)
	}
}
