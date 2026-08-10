package service

import (
	"context"
	"errors"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestResolveProjectGatesUsesProjectPolicyAndLegacyDefault(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	got, err := s.ResolveProjectGates(context.Background(), "example", "implementation")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != model.WorkflowGateFormat || got[1] != model.WorkflowGateCheck || got[2] != model.WorkflowGateTest {
		t.Fatalf("gates=%v", got)
	}
}

func TestExecuteProjectGatesUsesServerOwnedResults(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	calls := 0
	s.gateExecutor = func(_ context.Context, root string, names []string) ([]model.CompletionGateResult, error) {
		calls++
		if root == "" || len(names) != 3 {
			t.Fatalf("root=%q names=%v", root, names)
		}
		return []model.CompletionGateResult{{ID: "format", ExitCode: 0}, {ID: "check", ExitCode: 0}, {ID: "test", ExitCode: 0}}, nil
	}
	results, err := s.ExecuteProjectGates(context.Background(), "example", "implementation", s.Config.Projects["example"].Root)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(results) != 3 || results[0].ID != "format" || results[1].ID != "check" || results[2].ID != "test" {
		t.Fatalf("calls=%d results=%#v", calls, results)
	}
}

func TestRunFinalizeGateFailureDoesNotPublishState(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "server-gate-failure")
	completion := validCompletion(task, run)
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	s.gateExecutor = func(context.Context, string, []string) ([]model.CompletionGateResult, error) {
		return nil, errors.New("server check failed")
	}
	ctx := context.Background()
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID: run.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: before,
		},
	}); err == nil {
		t.Fatal("finalization accepted a failed server gate")
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("server gate failure mutated Hub: before=%s after=%s err=%v", before, after, err)
	}
	active, err := s.RunRead(ctx, run.ID)
	if err != nil || active.Status != "awaiting_result" {
		t.Fatalf("server gate failure changed run state: %#v %v", active, err)
	}
	if _, err := s.RunReport(ctx, run.ID); err == nil {
		t.Fatal("server gate failure published a report")
	}
}
