package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestRunCancelAcknowledgeNoMutationRejectsWorktreeProofFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, *Service, model.Task)
	}{
		{name: "wrong branch", setup: func(t *testing.T, s *Service, _ model.Task) {
			testutil.Git(t, s.Config.Projects["example"].Root, "switch", "main")
		}},
		{name: "dirty worktree", setup: func(t *testing.T, s *Service, _ model.Task) {
			if err := os.WriteFile(filepath.Join(s.Config.Projects["example"].Root, "uncommitted.txt"), []byte("dirty\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "changed HEAD", setup: func(t *testing.T, s *Service, _ model.Task) {
			root := s.Config.Projects["example"].Root
			if err := os.WriteFile(filepath.Join(root, "committed.txt"), []byte("changed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, root, "add", "committed.txt")
			testutil.Git(t, root, "commit", "-m", "test: change task head")
		}},
		{name: "divergent upstream", setup: func(t *testing.T, s *Service, task model.Task) {
			root := s.Config.Projects["example"].Root
			testutil.Git(t, root, "switch", "-c", "upstream-divergence")
			if err := os.WriteFile(filepath.Join(root, "upstream.txt"), []byte("upstream\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, root, "add", "upstream.txt")
			testutil.Git(t, root, "commit", "-m", "test: diverge upstream")
			testutil.Git(t, root, "push", "origin", "upstream-divergence:"+task.Branch)
			testutil.Git(t, root, "switch", task.Branch)
			testutil.Git(t, root, "branch", "--set-upstream-to=origin/"+task.Branch, task.Branch)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, task, run := acknowledgedCancel(t, "feature/cancel-worktree-"+strings.ReplaceAll(test.name, " ", "-"))
			test.setup(t, s, task)
			expectedError := "repository"
			if test.name == "divergent upstream" {
				expectedError = "upstream"
			}
			expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), expectedError)
		})
	}
}

func TestRunCancelAcknowledgeNoMutationRejectsTerminalAndHistoricalRuns(t *testing.T) {
	t.Run("terminal", func(t *testing.T) {
		s, _, run := acknowledgedCancel(t, "feature/cancel-terminal")
		mutateCancelRun(t, s, run, func(current *model.Run) { current.Status = "failed" })
		expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), "status")
	})

	t.Run("historical", func(t *testing.T) {
		s, task, run := acknowledgedCancel(t, "feature/cancel-historical")
		fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Replace(string(fixture), `"id": "11111111-1111-4111-8111-111111111111"`, `"id": "`+run.ID+`"`, 1)
		text = strings.Replace(text, `"task_id": "historical-task"`, `"task_id": "`+task.ID+`"`, 1)
		text = strings.Replace(text, `"task_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"task_sha256": "`+task.SHA256+`"`, 1)
		revision := mustHubRevision(t, s)
		if _, err := s.Hub.Transact(context.Background(), revision, "test: install historical run", func(worktree string) ([]string, error) {
			path := s.runPath(run.ProjectID, run.ID)
			return []string{path}, hub.WriteText(worktree, path, text)
		}); err != nil {
			t.Fatal(err)
		}
		expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), "")
	})
}

func TestRunCancelAcknowledgeNoMutationRejectsStaleExpectedRevision(t *testing.T) {
	s, task, run := acknowledgedCancel(t, "feature/cancel-stale-revision")
	stale := mustHubRevision(t, s)
	plan, err := s.PlanRead(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Revision++
	plan.UpdatedBy = "concurrent-test"
	plan.UpdatedAt = time.Now().UTC()
	if _, err := s.Hub.Transact(context.Background(), stale, "test: concurrent plan update", func(worktree string) ([]string, error) {
		path := s.planPath(task.ProjectID)
		return []string{path}, hub.WriteJSON(worktree, path, plan)
	}); err != nil {
		t.Fatal(err)
	}
	current := mustHubRevision(t, s)
	if current == stale {
		t.Fatal("concurrent test did not advance hub revision")
	}
	expectCancelAckReject(t, s, run.ID, stale, "")
}

func TestRunCancelAcknowledgeNoMutationRejectsBusyProjectLock(t *testing.T) {
	s, _, run := acknowledgedCancel(t, "feature/cancel-busy-lock")
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-example")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), "lock")
}

func mustRunID(t *testing.T, s *Service) string {
	t.Helper()
	runs, err := s.RunList(context.Background(), "example")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%#v err=%v", runs, err)
	}
	return runs[0].ID
}
