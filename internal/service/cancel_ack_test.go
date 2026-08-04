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

func TestRunCancelAcknowledgeNoMutationTerminalizesWithoutReport(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "feature/cancel-no-mutation")
	setCancelDeliveryScript(t, s, "cancel acknowledged\n", "", 0)
	ctx := context.Background()
	run, err := s.RunRead(ctx, mustRunID(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCancel(ctx, run.ID, mustHubRevision(t, s)); err != nil {
		t.Fatal(err)
	}
	run, err = s.RunRead(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := s.TaskReadRecord(ctx, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeState.State.Status != "dispatched" || run.Status != "cancel_requested" || run.DispatchExitCode == nil || *run.DispatchExitCode != 0 {
		t.Fatalf("unexpected cancellation state: run=%#v task=%#v", run, beforeState.State)
	}
	result, err := s.RunCancelAcknowledgeNoMutation(ctx, run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "cancelled_no_mutation" {
		t.Fatalf("status=%q", result.Status)
	}
	after, err := s.RunRead(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "failed" || after.FinishedAt == nil || after.DispatchExitCode == nil || *after.DispatchExitCode != 0 || after.DispatchStdout != run.DispatchStdout || after.DispatchStderr != run.DispatchStderr {
		t.Fatalf("cancellation evidence or terminal state changed incorrectly: %#v", after)
	}
	record, err := s.TaskReadRecord(ctx, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State.Status != "ready" {
		t.Fatalf("task state=%q", record.State.Status)
	}
	plan, err := s.PlanRead(ctx, run.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ActiveTaskID != "" || plan.ActiveRunID != "" {
		t.Fatalf("plan pointers remain: %#v", plan)
	}
	if _, err := s.Hub.ReadFile(ctx, s.reportPath(run.ProjectID, run.ID)); err == nil || !IsNotFound(err) {
		t.Fatalf("unexpected report result: %v", err)
	}
	if _, err := os.Lstat(run.CompletionPath); err == nil || !os.IsNotExist(err) {
		t.Fatalf("unexpected completion result: %v", err)
	}
}

func TestRunCancelAcknowledgeNoMutationRequiresRunRepositoryIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*model.Run, model.Task)
	}{
		{name: "branch", edit: func(run *model.Run, task model.Task) { run.Branch = task.Branch + "-other" }},
		{name: "base revision", edit: func(run *model.Run, task model.Task) { run.BaseRevision = strings.Repeat("c", 40) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, task, _, _ := dispatchedRun(t, "feature/cancel-run-identity-"+strings.ReplaceAll(test.name, " ", "-"))
			setCancelDeliveryScript(t, s, "cancel acknowledged\n", "", 0)
			ctx := context.Background()
			run, err := s.RunRead(ctx, mustRunID(t, s))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunCancel(ctx, run.ID, mustHubRevision(t, s)); err != nil {
				t.Fatal(err)
			}
			current, err := s.RunRead(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			test.edit(&current, task)
			before, err := s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Hub.Transact(ctx, before, "test: alter run repository identity", func(worktree string) ([]string, error) {
				path := s.runPath(current.ProjectID, current.ID)
				return []string{path}, hub.WriteJSON(worktree, path, current)
			}); err != nil {
				t.Fatal(err)
			}
			before, err = s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunCancelAcknowledgeNoMutation(ctx, current.ID, before); err == nil || !strings.Contains(err.Error(), "repository identity") {
				t.Fatalf("repository identity mismatch was accepted: %v", err)
			}
			after, err := s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("rejection mutated hub: before=%s after=%s", before, after)
			}
		})
	}
}

func TestRunCancelAcknowledgeNoMutationRequiresNonEmptyStdoutAndEmptyStderr(t *testing.T) {
	for _, test := range []struct {
		name   string
		stdout string
		stderr string
	}{
		{name: "empty stdout", stdout: "", stderr: ""},
		{name: "whitespace stdout", stdout: " \n\t", stderr: ""},
		{name: "stderr", stdout: "cancel acknowledged\n", stderr: "warning\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, _, _ := dispatchedRun(t, "feature/cancel-delivery-"+strings.ReplaceAll(test.name, " ", "-"))
			setCancelDeliveryScript(t, s, "cancel acknowledged\n", test.stderr, 0)
			ctx := context.Background()
			run, err := s.RunRead(ctx, mustRunID(t, s))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunCancel(ctx, run.ID, mustHubRevision(t, s)); err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(test.stdout) == "" {
				current, readErr := s.RunRead(ctx, run.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				current.DispatchStdout = test.stdout
				beforeDeliveryMutation, revisionErr := s.Hub.RemoteRevision(ctx)
				if revisionErr != nil {
					t.Fatal(revisionErr)
				}
				if _, writeErr := s.Hub.Transact(ctx, beforeDeliveryMutation, "test: clear cancellation stdout", func(worktree string) ([]string, error) {
					path := s.runPath(current.ProjectID, current.ID)
					return []string{path}, hub.WriteJSON(worktree, path, current)
				}); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			before, err := s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunCancelAcknowledgeNoMutation(ctx, run.ID, before); err == nil {
				t.Fatal("invalid cancellation delivery proof was accepted")
			}
			after, err := s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("rejection mutated hub: before=%s after=%s", before, after)
			}
		})
	}
}

func TestRunCancelAcknowledgeNoMutationRejectsFailedDeliveryWithoutMutation(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "feature/cancel-failed-delivery")
	setCancelDeliveryScript(t, s, "cancel acknowledged\n", "", 0)
	ctx := context.Background()
	run, err := s.RunRead(ctx, mustRunID(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCancel(ctx, run.ID, mustHubRevision(t, s)); err != nil {
		t.Fatal(err)
	}
	failed := run
	failed.Status = "cancel_requested"
	code := 7
	failed.DispatchExitCode = &code
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(ctx, before, "test: failed cancellation delivery", func(worktree string) ([]string, error) {
		path := s.runPath(failed.ProjectID, failed.ID)
		return []string{path}, hub.WriteJSON(worktree, path, failed)
	}); err != nil {
		t.Fatal(err)
	}
	before, err = s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCancelAcknowledgeNoMutation(ctx, failed.ID, before); err == nil || !strings.Contains(err.Error(), "delivery") {
		t.Fatalf("failed delivery was accepted: %v", err)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("rejection mutated hub: before=%s after=%s", before, after)
	}
}

func setCancelDeliveryScript(t *testing.T, s *Service, stdout, stderr string, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s' '" + strings.ReplaceAll(stdout, "'", "'\\''") + "'\nprintf '%s' '" + strings.ReplaceAll(stderr, "'", "'\\''") + "' >&2\nexit " + string(rune('0'+exitCode)) + "\n"
	if err := os.WriteFile(s.Airelay.Command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func acknowledgedCancel(t *testing.T, branch string) (*Service, model.Task, model.Run) {
	t.Helper()
	s, task, run, _ := dispatchedRun(t, branch)
	setCancelDeliveryScript(t, s, "cancel acknowledged\n", "", 0)
	if _, err := s.RunCancel(context.Background(), run.ID, mustHubRevision(t, s)); err != nil {
		t.Fatal(err)
	}
	run, err := s.RunRead(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, task, run
}

func expectCancelAckReject(t *testing.T, s *Service, runID, expected, message string) {
	t.Helper()
	before := mustHubRevision(t, s)
	if _, err := s.RunCancelAcknowledgeNoMutation(context.Background(), runID, expected); err == nil || (message != "" && !strings.Contains(err.Error(), message)) {
		t.Fatalf("acknowledgement rejection mismatch: %v", err)
	}
	after := mustHubRevision(t, s)
	if after != before {
		t.Fatalf("rejection mutated hub: before=%s after=%s", before, after)
	}
}

func mutateCancelRun(t *testing.T, s *Service, run model.Run, edit func(*model.Run)) {
	t.Helper()
	edit(&run)
	revision := mustHubRevision(t, s)
	if _, err := s.Hub.Transact(context.Background(), revision, "test: mutate cancellation run", func(worktree string) ([]string, error) {
		path := s.runPath(run.ProjectID, run.ID)
		return []string{path}, hub.WriteJSON(worktree, path, run)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunCancelAcknowledgeNoMutationRejectsMissingFailedAndStderrDelivery(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*model.Run)
	}{
		{name: "missing exit code", edit: func(run *model.Run) { run.DispatchExitCode = nil }},
		{name: "failed exit code", edit: func(run *model.Run) { code := 7; run.DispatchExitCode = &code }},
		{name: "non-empty stderr", edit: func(run *model.Run) { run.DispatchStderr = "warning\n" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, run := acknowledgedCancel(t, "feature/cancel-delivery-proof-"+strings.ReplaceAll(test.name, " ", "-"))
			mutateCancelRun(t, s, run, test.edit)
			expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), "delivery")
		})
	}
}

func TestRunCancelAcknowledgeNoMutationRejectsTaskStateAndPlanMismatch(t *testing.T) {
	t.Run("task state", func(t *testing.T) {
		s, task, run := acknowledgedCancel(t, "feature/cancel-task-state")
		state, err := s.taskState(context.Background(), task)
		if err != nil {
			t.Fatal(err)
		}
		state.Status = "ready"
		state.UpdatedAt = time.Now().UTC()
		revision := mustHubRevision(t, s)
		if _, err := s.Hub.Transact(context.Background(), revision, "test: mismatch task state", func(worktree string) ([]string, error) {
			path := s.taskStatePath(task.ProjectID, task.ID)
			return []string{path}, hub.WriteJSON(worktree, path, state)
		}); err != nil {
			t.Fatal(err)
		}
		expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), "task state")
	})

	t.Run("plan pointers", func(t *testing.T) {
		s, task, run := acknowledgedCancel(t, "feature/cancel-plan-mismatch")
		plan, err := s.PlanRead(context.Background(), task.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		plan.ActiveRunID = ""
		plan.Revision++
		plan.UpdatedAt = time.Now().UTC()
		revision := mustHubRevision(t, s)
		if _, err := s.Hub.Transact(context.Background(), revision, "test: mismatch plan pointers", func(worktree string) ([]string, error) {
			path := s.planPath(task.ProjectID)
			return []string{path}, hub.WriteJSON(worktree, path, plan)
		}); err != nil {
			t.Fatal(err)
		}
		expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), "plan")
	})
}

func TestRunCancelAcknowledgeNoMutationRejectsCompletionFileAndSymlink(t *testing.T) {
	for _, test := range []struct {
		name string
		make func(string) error
	}{
		{name: "file", make: func(path string) error { return os.WriteFile(path, []byte("completion"), 0o600) }},
		{name: "symlink", make: func(path string) error { return os.Symlink(filepath.Join(filepath.Dir(path), "missing-target"), path) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, run := acknowledgedCancel(t, "feature/cancel-completion-"+test.name)
			if err := test.make(run.CompletionPath); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(run.CompletionPath)
			expectCancelAckReject(t, s, run.ID, mustHubRevision(t, s), "completion")
		})
	}
}

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
