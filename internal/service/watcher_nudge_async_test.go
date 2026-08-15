package service

import (
	"context"
	"testing"
	"time"
)

func TestWatcherNudgeReturnsBoundedReceipt(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	started := time.Now()
	receipt, err := s.WatcherNudgeAsync(context.Background(), WatcherNudgeInput{ProjectID: "example", Text: "bounded nudge"})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("watcher/nudge initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("watcher/nudge initiation latency: %s", elapsed)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		completed, statusErr := s.WatcherNudgeOperationStatus(context.Background(), receipt.OperationID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watcher/nudge worker did not reach a terminal receipt")
}
