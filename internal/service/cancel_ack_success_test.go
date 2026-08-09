package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
		want string
		edit func(*model.Run, model.Task)
	}{
		{name: "branch", want: "repository identity", edit: func(run *model.Run, task model.Task) { run.Branch = task.Branch + "-other" }},
		{name: "base revision", want: "run execution base", edit: func(run *model.Run, task model.Task) { run.BaseRevision = strings.Repeat("c", 40) }},
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
			if _, err := s.RunCancelAcknowledgeNoMutation(ctx, current.ID, before); err == nil || !strings.Contains(err.Error(), test.want) {
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
