package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTrainGatesScopedFormatIgnoresBaselineFormattingDebt(t *testing.T) {
	s, root, base, candidate := trainScopedFormatFixture(t, []byte("package changed\n\nvar value = 1\n"))
	var formattedPaths []string
	s.formatExecutor = func(_ context.Context, _ string, paths []string) error {
		formattedPaths = append([]string{}, paths...)
		return nil
	}
	s.gateExecutorWithProjectCommands = func(_ context.Context, _ string, names []string, _ model.ProjectGateCommands, _ string) ([]model.CompletionGateResult, error) {
		return fakeReceiptResults(names), nil
	}
	if _, err := s.executeTrainGatesWithScopedFormat(context.Background(), "example", s.Config.Projects["example"], base, candidate); err != nil {
		t.Fatalf("baseline formatting debt blocked Train gates: %v", err)
	}
	if !reflect.DeepEqual(formattedPaths, []string{"changed.go"}) {
		t.Fatalf("format scope=%v want [changed.go]", formattedPaths)
	}
	changed, err := s.Git.ChangedFiles(context.Background(), root, base, candidate)
	if err != nil || !reflect.DeepEqual(changed, []string{"changed.go"}) {
		t.Fatalf("Train delta=%v err=%v", changed, err)
	}
}

func TestTrainGatesScopedFormatRejectsChangedMisformattedGoWithoutMutation(t *testing.T) {
	s, root, base, candidate := trainScopedFormatFixture(t, []byte("package changed\n\nvar value = 1\n"))
	s.formatExecutor = func(_ context.Context, _ string, paths []string) error {
		if !reflect.DeepEqual(paths, []string{"changed.go"}) {
			t.Fatalf("format scope=%v want [changed.go]", paths)
		}
		return errors.New("changed Go file is misformatted")
	}
	before, err := os.ReadFile(filepath.Join(root, "changed.go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.executeTrainGatesWithScopedFormat(context.Background(), "example", s.Config.Projects["example"], base, candidate); err == nil {
		t.Fatal("misformatted changed Go file passed scoped Train formatting")
	}
	after, err := os.ReadFile(filepath.Join(root, "changed.go"))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("candidate bytes changed: err=%v", err)
	}
}

func TestTrainGatesFailClosedOnRepositoryMutation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(t *testing.T, root string)
		returnErr bool
	}{
		{name: "worktree", mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "gate-mutated.txt"), []byte("worktree\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "index", mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "gate-mutated.txt"), []byte("index\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, root, "add", "gate-mutated.txt")
		}},
		{name: "head-tree", mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "gate-mutated.txt"), []byte("head\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, root, "add", "gate-mutated.txt")
			testutil.Git(t, root, "commit", "--message", "gate mutation")
		}},
		{name: "branch", mutate: func(t *testing.T, root string) {
			testutil.Git(t, root, "checkout", "-b", "gate-mutation")
		}},
		{name: "error-after-mutation", returnErr: true, mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "gate-mutated.txt"), []byte("error\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, root, "add", "gate-mutated.txt")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, root, base, candidate := trainScopedFormatFixture(t, []byte("package changed\n\nvar value = 1\n"))
			s.formatExecutor = func(context.Context, string, []string) error { return nil }
			s.gateExecutorWithProjectCommands = func(_ context.Context, _ string, names []string, _ model.ProjectGateCommands, _ string) ([]model.CompletionGateResult, error) {
				tc.mutate(t, root)
				if tc.returnErr {
					return fakeReceiptResults(names), errors.New("gate failed after mutation")
				}
				return fakeReceiptResults(names), nil
			}
			_, err := s.executeTrainGatesWithScopedFormat(context.Background(), "example", s.Config.Projects["example"], base, candidate)
			if err == nil || !strings.Contains(err.Error(), "verification gate mutated repository state") {
				t.Fatalf("mutation error=%v", err)
			}
		})
	}
}

func TestMergeGateResultsRejectsDuplicateMissingUnexpectedEvidence(t *testing.T) {
	expected := []string{"format", "check", "test"}
	valid := func() ([]model.CompletionGateResult, error) {
		format := fakeReceiptResults([]string{"format"})
		remaining := fakeReceiptResults([]string{"check", "test"})
		for i := range format {
			format[i].AggregateMS = 100
			format[i].Warnings = []string{gates.GateOptimizationWarning + ": aggregate_ms=100", "gate-specific warning"}
		}
		for i := range remaining {
			remaining[i].AggregateMS = 200
			remaining[i].Warnings = []string{gates.GateOptimizationWarning + ": aggregate_ms=200"}
		}
		return mergeGateResults(expected, format, remaining, 12)
	}
	if results, err := valid(); err != nil || len(results) != 3 || results[0].AggregateMS != 12 || results[1].AggregateMS != 0 || results[2].AggregateMS != 0 || !reflect.DeepEqual(results[0].Warnings, []string{"gate-specific warning"}) || len(results[1].Warnings) != 0 || len(results[2].Warnings) != 0 {
		t.Fatalf("valid evidence=%v err=%v", results, err)
	}
	for name, remaining := range map[string][]model.CompletionGateResult{
		"duplicate":  {{ID: "check"}, {ID: "check"}, {ID: "test"}},
		"missing":    {{ID: "check"}},
		"unexpected": {{ID: "check"}, {ID: "test"}, {ID: "release"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeGateResults(expected, nil, remaining, 12); err == nil {
				t.Fatalf("invalid evidence accepted: %v", remaining)
			}
		})
	}
}

func trainScopedFormatFixture(t *testing.T, changed []byte) (*Service, string, string, string) {
	t.Helper()
	s, _, _ := testService(t)
	root := s.Config.Projects["example"].Root
	if err := os.WriteFile(filepath.Join(root, "baseline.go"), []byte("package baseline\n\nvar value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "baseline.go")
	testutil.Git(t, root, "commit", "--message", "baseline formatter debt")
	base, _, _, err := s.Git.CurrentHead(context.Background(), s.Config.Projects["example"])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "changed.go"), changed, 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "changed.go")
	testutil.Git(t, root, "commit", "--message", "Train candidate")
	candidate, _, _, err := s.Git.CurrentHead(context.Background(), s.Config.Projects["example"])
	if err != nil {
		t.Fatal(err)
	}
	return s, root, base, candidate
}
