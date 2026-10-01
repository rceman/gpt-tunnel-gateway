package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// TestTSK682RetirementPublishCannotBlockOnPinnedSnapshot is the GTW-JRN15
// regression: TSK681 pinned hub-repository.lock across the whole retirement
// sync, so a publish's post-Transact ReadSnapshot parked forever on a
// blocking LOCK_SH that ignored ctx. Now shared-lock acquisition is
// ctx-bounded and the post-ready flow publishes only after the pinned
// snapshot closes.
func TestTSK682RetirementPublishCannotBlockOnPinnedSnapshot(t *testing.T) {
	ctx := context.Background()
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	s.Config.Debug.Enabled = true
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	s.localState = db
	if _, err := s.DebugRetireProject(ctx, "example", "retire for snapshot regression"); err != nil {
		t.Fatalf("seed retirement: %v", err)
	}

	// Legacy monolithic shape under a pinned snapshot: the Transact cannot
	// take the exclusive lock we hold, and its post-transaction shared-lock
	// verification must fail bounded instead of parking forever.
	snapshot, err := s.Hub.FreshReadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pinCtx, pinCancel := context.WithTimeout(hub.WithReadSnapshot(ctx, snapshot), 2*time.Second)
	defer pinCancel()
	done := make(chan error, 1)
	go func() { done <- s.SyncProjectRetirements(pinCtx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("retirement publish succeeded while its pinned snapshot held the repository lock")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("retirement publish parked on a blocking shared lock under the pinned snapshot")
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}

	// Canonical split used by the post-ready path: all Hub reads under the
	// pinned snapshot, Transact/publish only after it closes.
	snapshot, err = s.Hub.FreshReadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.CollectSyncedProjectRetirements(hub.WithReadSnapshot(ctx, snapshot))
	if closeErr := snapshot.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatalf("collect under pinned snapshot: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("collect deferred no retirement publish")
	}
	if err := s.PublishSyncedProjectRetirements(ctx, pending); err != nil {
		t.Fatalf("publish after snapshot close: %v", err)
	}
	if _, found, err := s.readHubProjectRetirement(ctx, "example"); err != nil || !found {
		t.Fatalf("Hub retirement marker after publish: found=%v err=%v", found, err)
	}
}
