package gates

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestResolveDefaultsToTheThreeStandardGates(t *testing.T) {
	got, err := Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{model.WorkflowGateFormat, model.WorkflowGateCheck, model.WorkflowGateTest}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("gates=%v want=%v", got, want)
	}
	if _, err := Resolve([]string{"format", "format"}); err == nil {
		t.Fatal("duplicate gate accepted")
	}
	if _, err := Resolve([]string{"gates"}); err == nil {
		t.Fatal("non-standard gate accepted")
	}
	got, err = Resolve([]string{"format"})
	if err != nil || len(got) != 1 || got[0] != model.WorkflowGateFormat {
		t.Fatalf("direct format resolution=%v err=%v", got, err)
	}
}

func TestExecuteWithProjectCommandsUsesTaskAndTrainDefinitions(t *testing.T) {
	var commands [][]string
	executor := Executor{Command: func(_ context.Context, _ string, name string, args ...string) (int, string, error) {
		commands = append(commands, append([]string{name}, args...))
		return 0, "", nil
	}}
	configured := model.DefaultProjectGateCommands()
	configured.Test.Task = model.ProjectGateCommand{Command: []string{"./scripts/test-task", "--affected"}}
	configured.Test.Train = model.ProjectGateCommand{Command: []string{"./scripts/test-train", "--full"}}
	if _, err := executor.ExecuteWithProjectCommands(context.Background(), "/repo", []string{"format", "check", "test"}, configured, "task"); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteWithProjectCommands(context.Background(), "/repo", []string{"test"}, configured, "train"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 4 || commands[2][0] != "./scripts/test-task" || commands[2][1] != "--affected" || commands[3][0] != "./scripts/test-train" || commands[3][1] != "--full" {
		t.Fatalf("project-owned command selection=%v", commands)
	}
}

func TestStaticCheckAllowsRepodexInitForce(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", filepath.Join(root, "scripts", "static-check.py"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("static checker rejected the safe repodex force flag: %v\n%s", err, output)
	}
}

func TestNativeTokenAdmissionReportsOffenders(t *testing.T) {
	report := TokenReport{
		Max:       TokenFile{Path: "large.go", Tokens: MaxTokens + 1},
		Offending: []TokenFile{{Path: "large.go", Tokens: MaxTokens + 1}},
	}
	e := Executor{
		Tokens: func(context.Context, string) (TokenReport, error) { return report, nil },
		Command: func(context.Context, string, string, ...string) (int, string, error) {
			t.Fatal("command ran after native token admission failure")
			return 0, "", nil
		},
	}
	if _, err := e.Execute(context.Background(), "/repo", []string{"check"}); err == nil || !strings.Contains(err.Error(), "large.go") {
		t.Fatalf("native overflow was not reported: %v", err)
	}
}

func TestCheckGateWorksWithoutExternalTokenTools(t *testing.T) {
	root := t.TempDir()
	if err := runGateGit(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "small.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGateGit(root, "add", "small.go"); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(gitPath))
	e := Executor{
		Tokens: CountTokens,
		Command: func(context.Context, string, string, ...string) (int, string, error) {
			return 0, "", nil
		},
	}
	results, err := e.Execute(context.Background(), root, []string{"check"})
	if err != nil || len(results) != 1 || results[0].ExitCode != 0 {
		t.Fatalf("check results=%#v err=%v", results, err)
	}
}

func runGateGit(root string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	return cmd.Run()
}

func TestExecutorAlwaysRunsMandatoryTokenAdmission(t *testing.T) {
	tokenCalls := 0
	commandCalls := 0
	e := Executor{
		Tokens: func(context.Context, string) (TokenReport, error) {
			tokenCalls++
			return TokenReport{
				Max: TokenFile{
					Path:   "README.md",
					Tokens: 2947,
				},
			}, nil
		},
		Command: func(context.Context, string, string, ...string) (int, string, error) {
			commandCalls++
			return 0, "", nil
		},
	}
	results, err := e.Execute(context.Background(), "/repo", []string{"check"})
	if err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 || commandCalls != 1 || len(results) != 1 || results[0].ExitCode != 0 {
		t.Fatalf("token=%d command=%d results=%#v", tokenCalls, commandCalls, results)
	}
	if _, err := (Executor{
		Tokens: func(context.Context, string) (TokenReport, error) {
			return TokenReport{
				Max: TokenFile{
					Path:   "large.go",
					Tokens: MaxTokens + 1,
				},
			}, nil
		},
		Command: func(context.Context, string, string, ...string) (int, string, error) {
			t.Fatal("command ran after token admission failure")
			return 0, "", nil
		},
	}).Execute(context.Background(), "/repo", []string{"check"}); err == nil {
		t.Fatal("token overflow did not block gate")
	}
}

func TestExecutorUsesScopedAndLegacyFullTestCommands(t *testing.T) {
	var calls [][]string
	e := Executor{
		Tokens: func(context.Context, string) (TokenReport, error) {
			return TokenReport{Max: TokenFile{Path: "small.go", Tokens: 1}}, nil
		},
		Command: func(_ context.Context, _ string, name string, args ...string) (int, string, error) {
			calls = append(calls, append([]string{name}, args...))
			return 0, "", nil
		},
	}
	if _, err := e.ExecuteWithScope(context.Background(), "/repo", []string{"test"}, TestScope{
		Mode:     TestScopePackages,
		Packages: []string{"./z", "./a"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), "/repo", []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !reflect.DeepEqual(calls[0], []string{"go", "test", "./a", "./z", "-count=1"}) || !reflect.DeepEqual(calls[1], []string{"go", "test", "./...", "-count=1"}) {
		t.Fatalf("test commands=%v", calls)
	}
}

func TestProjectTaskCommandUsesAffectedPackagesAndTrainStaysFull(t *testing.T) {
	var calls [][]string
	e := Executor{Command: func(_ context.Context, _ string, name string, args ...string) (int, string, error) {
		calls = append(calls, append([]string{name}, args...))
		return 0, "", nil
	}}
	commands := model.DefaultProjectGateCommands()
	if _, err := e.ExecuteWithProjectCommandsAndScope(context.Background(), "/repo", []string{"test"}, commands, "task", TestScope{
		Mode:     TestScopePackages,
		Packages: []string{"./internal/service"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecuteWithProjectCommandsAndScope(context.Background(), "/repo", []string{"test"}, commands, "train", FullTestScope()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[0], []string{"go", "test", "./internal/service", "-count=1"}) || !reflect.DeepEqual(calls[1], []string{"go", "test", "./...", "-count=1"}) {
		t.Fatalf("project task/train commands=%v", calls)
	}
}

func TestGateTimingWarningIsNonfatalAndBounded(t *testing.T) {
	result := timedGateResult(model.WorkflowGateTest, 0, gateOptimizationBudget+time.Millisecond)
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], GateOptimizationWarning) || result.DurationMS < 30000 {
		t.Fatalf("timing warning=%#v", result)
	}
	results := []model.CompletionGateResult{result}
	annotateGateAggregate(results, gateOptimizationBudget+time.Millisecond)
	if results[0].AggregateMS < 30000 || len(results[0].Warnings) != 2 {
		t.Fatalf("aggregate timing=%#v", results[0])
	}
}

func TestTestGateCommandContractPreservesFullCompatibility(t *testing.T) {
	legacy, err := TestGateContractDigest([]string{"format", "check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	full, err := TestGateCommandContractDigest([]string{"format", "check", "test"}, FullTestScope())
	if err != nil || full != legacy {
		t.Fatalf("full contract changed: legacy=%s full=%s err=%v", legacy, full, err)
	}
	scoped, err := TestGateCommandContractDigest([]string{"format", "check", "test"}, TestScope{
		Mode:     TestScopePackages,
		Packages: []string{"./internal/gates"},
	})
	if err != nil || scoped == legacy {
		t.Fatalf("scoped contract did not differ: scoped=%s legacy=%s err=%v", scoped, legacy, err)
	}
}

func TestExecutorIncludesFullOutputForAnyFailedCommand(t *testing.T) {
	e := Executor{
		Tokens: func(context.Context, string) (TokenReport, error) {
			return TokenReport{Max: TokenFile{Path: "small.txt", Tokens: 1}}, nil
		},
		Command: func(context.Context, string, string, ...string) (int, string, error) {
			return 1, "FAIL custom-language: assertion failed\nsecond diagnostic line", errors.New("exit status 1")
		},
	}
	_, err := e.Execute(context.Background(), "/repo", []string{"test"})
	if err == nil || !strings.Contains(err.Error(), "gate test failed") || !strings.Contains(err.Error(), "FAIL custom-language: assertion failed") || !strings.Contains(err.Error(), "second diagnostic line") {
		t.Fatalf("failed gate omitted bounded command output: %v", err)
	}
}

func TestFixedCommandTailCapsOversizedMultilineOutput(t *testing.T) {
	code, output, err := fixedCommand(context.Background(), t.TempDir(), "sh", "-c", "i=0; while [ $i -lt 20000 ]; do printf 'line-%05d\\n' \"$i\"; i=$((i+1)); done; printf 'FINAL-CAUSAL-LINE\\n'; exit 1")
	if err == nil || code != 1 {
		t.Fatalf("oversized command result=%d err=%v", code, err)
	}
	if len(output) > maxGateOutputBytes {
		t.Fatalf("captured output exceeded cap: %d > %d", len(output), maxGateOutputBytes)
	}
	if !strings.HasPrefix(output, gateOutputTruncationMarker) {
		t.Fatalf("missing truncation marker: %q", output[:min(len(output), 80)])
	}
	if !strings.Contains(output, "FINAL-CAUSAL-LINE") {
		t.Fatal("tail output lost final causal line")
	}
	if strings.Contains(output, "line-00000") {
		t.Fatal("tail output retained the discarded beginning")
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
