package gates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
