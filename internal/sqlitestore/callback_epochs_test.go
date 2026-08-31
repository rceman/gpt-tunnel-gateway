package sqlitestore

import (
	"context"
	"testing"
	"time"
)

func TestCallbackEpochRequiresRealWorkAndSurvivesRestart(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	epoch := CallbackEpoch{ID: "agent-work-1", ProjectID: "example", SessionKey: "example_master", ArmedAt: time.Now().UTC()}
	if err := db.ArmCallbackEpoch(context.Background(), epoch); err != nil {
		t.Fatal(err)
	}
	if ready, err := db.ObserveCallbackEpoch(context.Background(), epoch.ID, "idle"); err != nil || ready {
		t.Fatalf("startup idle ready=%v err=%v", ready, err)
	}
	if ready, err := db.ObserveCallbackEpoch(context.Background(), epoch.ID, "running"); err != nil || ready {
		t.Fatalf("running ready=%v err=%v", ready, err)
	}
	if ready, err := db.ObserveCallbackEpoch(context.Background(), epoch.ID, "idle"); err != nil || ready {
		t.Fatalf("first idle ready=%v err=%v", ready, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if ready, err := db.ObserveCallbackEpoch(context.Background(), epoch.ID, "idle"); err != nil || !ready {
		t.Fatalf("second idle after restart ready=%v err=%v", ready, err)
	}
	claimed, err := db.ClaimCallbackEpoch(context.Background(), epoch.ID, time.Now().UTC())
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	claimed, err = db.ClaimCallbackEpoch(context.Background(), epoch.ID, time.Now().UTC())
	if err != nil || claimed {
		t.Fatalf("duplicate claim=%v err=%v", claimed, err)
	}
	pending, err := db.PendingCallbackEpochs(context.Background(), 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
}
