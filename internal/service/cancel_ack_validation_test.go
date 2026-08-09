package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
