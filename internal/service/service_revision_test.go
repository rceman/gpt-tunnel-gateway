package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestSyntheticInvalidPublishedBranchFailsAtomically(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "feature/synthetic-invalid")
	ctx := context.Background()
	project := s.Config.Projects["example"]
	remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
	moverParent := t.TempDir()
	mover := filepath.Join(moverParent, "mover")
	testutil.Git(t, moverParent, "clone", "--no-local", remote, mover)
	testutil.Git(t, mover, "config", "user.name", "Test User")
	testutil.Git(t, mover, "config", "user.email", "test@example.invalid")
	testutil.Git(t, mover, "switch", "--orphan", "unrelated")
	if err := os.WriteFile(filepath.Join(mover, "unrelated.txt"), []byte("unrelated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, mover, "add", "unrelated.txt")
	testutil.Git(t, mover, "commit", "-m", "unrelated published proof")
	testutil.Git(t, mover, "branch", "-M", task.Branch)
	testutil.Git(t, mover, "push", "origin", task.Branch)
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.failRun(ctx, run, task, "failed", "synthetic failure", before); err == nil || !strings.Contains(err.Error(), "not descended") {
		t.Fatalf("invalid published branch was accepted: %v", err)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("invalid published branch mutated hub: before=%s after=%s err=%v", before, after, err)
	}
	active, err := s.RunRead(ctx, run.ID)
	if err != nil || active.Status != "awaiting_result" {
		t.Fatalf("invalid published branch changed run: %#v %v", active, err)
	}
	state, err := s.taskState(ctx, task)
	if err != nil || state.Status != "dispatched" {
		t.Fatalf("invalid published branch changed task: %#v %v", state, err)
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil || plan.ActiveRunID != run.ID {
		t.Fatalf("invalid published branch changed plan: %#v %v", plan, err)
	}
	if _, err := s.RunReport(ctx, run.ID); err == nil {
		t.Fatal("invalid published branch created a report")
	}
}

func mustHubRevision(t *testing.T, s *Service) string {
	t.Helper()
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
