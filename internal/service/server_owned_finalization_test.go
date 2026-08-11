package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestRunFinalizeBuildsServerOwnedCompletionWithoutAgentFile(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "server-owned-finalize")
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "server-owned.txt"), []byte("server-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	publishServerOwnedChange(t, project.Root, run.Branch, "server-owned.txt", "server-owned finalization")
	if _, err := os.Lstat(run.CompletionPath); !os.IsNotExist(err) {
		t.Fatalf("completion existed before canonical finalization: err=%v", err)
	}

	report, result, err := s.RunFinalize(context.Background(), FinalizeInput{
		RunID:   run.ID,
		Summary: "Implemented and verified the task.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "TASK_FINALIZED" || report.Status != "succeeded" {
		t.Fatalf("unexpected finalization result: report=%#v result=%#v", report, result)
	}
	data, err := fsutil.ReadFileBounded(run.CompletionPath, s.Config.MaxReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := model.ParseCompletion(data, task)
	if err != nil {
		t.Fatal(err)
	}
	if completion.RunID != run.ID || completion.TaskSHA256 != task.SHA256 || completion.Summary != "Implemented and verified the task." {
		t.Fatalf("server-owned completion lost durable identity or summary: %#v", completion)
	}

	hubBefore, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	retry, retryResult, err := s.RunFinalize(context.Background(), FinalizeInput{
		RunID:   run.ID,
		Summary: "Implemented and verified the task.",
	})
	if err != nil {
		t.Fatal(err)
	}
	hubAfter, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hubBefore != hubAfter || retryResult.Status != "TASK_FINALIZED" || retry.RunID != report.RunID {
		t.Fatalf("finalization retry was not idempotent: before=%s after=%s retry=%#v result=%#v", hubBefore, hubAfter, retry, retryResult)
	}
}

func TestRunFinalizeServerOwnedGateFailureDoesNotPublishState(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "server-owned-gate-failure")
	s.gateExecutor = func(context.Context, string, []string) ([]model.CompletionGateResult, error) {
		return nil, errors.New("server check failed")
	}
	ctx := context.Background()
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID:   run.ID,
		Summary: "Attempted finalization.",
	}); err == nil || !strings.Contains(err.Error(), "server check failed") {
		t.Fatalf("server-owned gate failure was not returned: %v", err)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("server-owned gate failure mutated Hub: before=%s after=%s err=%v", before, after, err)
	}
	if _, err := os.Lstat(run.CompletionPath); !os.IsNotExist(err) {
		t.Fatalf("server-owned gate failure created completion: %v", err)
	}
	active, err := s.RunRead(ctx, run.ID)
	if err != nil || active.Status != "awaiting_result" {
		t.Fatalf("server-owned gate failure changed run state: %#v %v", active, err)
	}
}

func TestRunFinalizeServerOwnedAtomicWriteFailureCanRetry(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "server-owned-atomic-retry")
	publishServerOwnedChange(t, s.Config.Projects["example"].Root, run.Branch, "atomic-retry.txt", "server-owned atomic retry")
	previous := completionOpenDirectory
	completionOpenDirectory = func(string) (completionDirectory, error) {
		return nil, errors.New("directory unavailable")
	}
	defer func() { completionOpenDirectory = previous }()
	ctx := context.Background()
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID:   run.ID,
		Summary: "Atomic retry proof.",
	}); err == nil || !strings.Contains(err.Error(), "open completion directory") {
		t.Fatalf("completion durability failure was not returned: %v", err)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("completion durability failure mutated Hub: before=%s after=%s err=%v", before, after, err)
	}
	if _, err := s.RunRead(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	completionOpenDirectory = previous
	if _, result, err := s.RunFinalize(ctx, FinalizeInput{
		RunID:   run.ID,
		Summary: "Atomic retry proof.",
	}); err != nil || result.Status != "TASK_FINALIZED" {
		t.Fatalf("same finalization did not retry successfully: result=%#v err=%v", result, err)
	}
}

func publishServerOwnedChange(t *testing.T, root, branch, filename, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filename), []byte("server-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", filename)
	testutil.Git(t, root, "commit", "-m", message)
	testutil.Git(t, root, "push", "origin", branch)
}

func TestRunFinalizeRequiresSummaryWithoutLegacyCompletion(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "server-owned-summary-required")
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(context.Background(), FinalizeInput{RunID: run.ID}); err == nil {
		t.Fatal("canonical finalization without summary was accepted")
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("missing summary mutated Hub: before=%s after=%s", before, after)
	}
	if _, err := os.Lstat(run.CompletionPath); !os.IsNotExist(err) {
		t.Fatalf("missing summary created completion: err=%v", err)
	}
}
