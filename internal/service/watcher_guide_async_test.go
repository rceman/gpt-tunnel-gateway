package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestWatcherGuideUpdateAsyncIsBoundedAndIdempotent(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := WatcherGuideUpdateInput{
		ProjectID: "example",
		Guide: model.WatcherGuide{
			SchemaVersion: model.WatcherGuideSchemaVersion,
			ProjectID:     "example",
			Revision:      1,
			Content:       CanonicalWatcherGuideContent,
			UpdatedBy:     "planner",
			UpdatedAt:     time.Now().UTC(),
		},
		ExpectedHubRevision: revision,
	}
	started := time.Now()
	first, err := s.WatcherGuideUpdateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("watcher guide initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("watcher guide initiation latency: %s", elapsed)
	}
	second, err := s.WatcherGuideUpdateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("watcher guide initiation was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed WatcherGuideMutationReceipt
	for time.Now().Before(deadline) {
		completed, err = s.WatcherGuideUpdateOperationStatus(context.Background(), first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Guide == nil || completed.Guide.Revision != 1 {
		t.Fatalf("watcher guide worker did not complete: %#v", completed)
	}
}
