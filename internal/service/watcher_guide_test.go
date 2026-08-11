package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestWatcherGuideRevisionedHubAuthority(t *testing.T) {
	s, revision, _ := testService(t)
	now := time.Now().UTC()
	guide := model.WatcherGuide{
		SchemaVersion: model.WatcherGuideSchemaVersion,
		ProjectID:     "example",
		Revision:      1,
		Content:       "Observe one bounded tick; terminal runs are authoritative.",
		UpdatedBy:     "planner",
		UpdatedAt:     now,
	}
	result, err := s.WatcherGuideUpdate(context.Background(), WatcherGuideUpdateInput{
		ProjectID:           "example",
		Guide:               guide,
		ExpectedHubRevision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.WatcherGuideRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || got.Content != guide.Content || result.Status != "updated" {
		t.Fatalf("unexpected watcher guide: %#v %#v", got, result)
	}
	guide.Revision = 2
	guide.Content = "Updated bounded guide."
	guide.UpdatedAt = now.Add(time.Second)
	if _, err := s.WatcherGuideUpdate(context.Background(), WatcherGuideUpdateInput{
		ProjectID:           "example",
		Guide:               guide,
		ExpectedHubRevision: result.Hub.After,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatcherGuideUpdate(context.Background(), WatcherGuideUpdateInput{
		ProjectID:           "example",
		Guide:               guide,
		ExpectedHubRevision: "bad-revision",
	}); err == nil || !strings.Contains(err.Error(), "HUB_REVISION_CONFLICT") {
		t.Fatalf("stale watcher guide update was not rejected: %v", err)
	}
}
