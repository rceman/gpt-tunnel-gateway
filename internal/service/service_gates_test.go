package service

import (
	"context"
	"testing"

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

func TestExecuteProjectGatesUsesProjectOwnedTaskAndTrainModes(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	var modes []string
	s.gateExecutorWithProjectCommands = func(_ context.Context, _ string, names []string, commands model.ProjectGateCommands, mode string) ([]model.CompletionGateResult, error) {
		modes = append(modes, mode)
		if commands.Test.Task.Command[0] == "" || commands.Test.Train.Command[0] == "" {
			t.Fatal("missing project-owned test command")
		}
		return fakeReceiptResults(names), nil
	}
	if _, err := s.ExecuteProjectGates(context.Background(), "example", "implementation", s.Config.Projects["example"].Root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecuteProjectGates(context.Background(), "example", "integration", s.Config.Projects["example"].Root); err != nil {
		t.Fatal(err)
	}
	if len(modes) != 2 || modes[0] != "task" || modes[1] != "train" {
		t.Fatalf("project gate modes=%v", modes)
	}
}
