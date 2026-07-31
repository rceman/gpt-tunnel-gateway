package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestRunSweepAndCancelDoNotOperateForeignGatewayRun(t *testing.T) {
	s, revision, _ := testService(t)
	now := time.Now().UTC().Add(-2 * time.Hour)
	run := model.Run{
		SchemaVersion: 1, ID: "foreign-run", TaskID: "foreign-task", TaskSHA256: strings.Repeat("a", 64),
		ProjectID: "example", GatewayID: "other_gateway", SessionKey: "example_master",
		Branch: "feature/foreign", BaseRevision: strings.Repeat("b", 40), Status: "awaiting_result",
		CompletionPath: "/tmp/completion.json", CreatedAt: now,
	}
	tx, err := s.Hub.Transact(context.Background(), revision, "test: foreign run", func(worktree string) ([]string, error) {
		path := s.runPath(run.ProjectID, run.ID)
		return []string{path}, hub.WriteJSON(worktree, path, run)
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tx
	sweep, err := s.RunSweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sweep.Checked != 0 || len(sweep.Items) != 0 {
		t.Fatalf("foreign run was swept: %#v", sweep)
	}
	if _, err := s.RunCancel(context.Background(), run.ID, ""); err == nil || !strings.Contains(err.Error(), "assigned to gateway other_gateway") {
		t.Fatalf("foreign run cancellation was not rejected: %v", err)
	}
}
