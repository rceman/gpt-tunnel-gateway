package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskFinalizeChangedFilesUnionsCommittedAndWorkingChanges(t *testing.T) {
	s, _, _ := testService(t)
	root := s.Config.Projects["example"].Root
	startHead, _, _, err := s.Git.CurrentHead(context.Background(), s.Config.Projects["example"])
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("committed.go")
	testutil.Git(t, root, "add", "committed.go")
	testutil.Git(t, root, "commit", "--message", "committed candidate")
	write("staged.go")
	testutil.Git(t, root, "add", "staged.go")
	write("working.go")
	head, _, _, err := s.Git.CurrentHead(context.Background(), s.Config.Projects["example"])
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.taskFinalizeChangedFiles(context.Background(), root, startHead, head)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"committed.go", "staged.go", "working.go"}
	formatted, err := existingGoFiles(root, got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(formatted, want) {
		t.Fatalf("changed files=%v want=%v", got, want)
	}
}

func TestExistingGoFilesSkipsDeletedAndNonRegularPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "present.go"), []byte("package present\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.go"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := existingGoFiles(root, []string{"deleted.go", "directory.go", "notes.txt", "present.go"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"present.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("existing Go files=%v want=%v", got, want)
	}
}

func TestTaskFinalizeGatesFailClosedOnCheckAndTestMutation(t *testing.T) {
	for _, returnErr := range []bool{false, true} {
		name := "success"
		if returnErr {
			name = "error"
		}
		t.Run(name, func(t *testing.T) {
			s, _, _ := testService(t)
			root := s.Config.Projects["example"].Root
			s.gateExecutorWithProjectCommands = func(_ context.Context, _ string, names []string, _ model.ProjectGateCommands, _ string) ([]model.CompletionGateResult, error) {
				if !reflect.DeepEqual(names, []string{"check", "test"}) {
					t.Fatalf("non-format gate names=%v", names)
				}
				if err := os.WriteFile(filepath.Join(root, "gate-mutated.go"), []byte("package mutated\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				testutil.Git(t, root, "add", "gate-mutated.go")
				if returnErr {
					return fakeReceiptResults(names), os.ErrInvalid
				}
				return fakeReceiptResults(names), nil
			}
			_, err := s.executeTaskFinalizeGatesWithSnapshot(context.Background(), "example", s.Config.Projects["example"], []string{"format", "check", "test"}, []string{"changed.go"}, gates.FullTestScope())
			if err == nil || !strings.Contains(err.Error(), "verification gate mutated repository state") {
				t.Fatalf("mutation error=%v", err)
			}
		})
	}
}

func TestTaskFinalizeFormatOnlyDoesNotExpandDefaultGates(t *testing.T) {
	s, _, _ := testService(t)
	s.formatExecutor = func(context.Context, string, []string) error { return nil }
	s.gateExecutorWithProjectCommandsAndScope = func(context.Context, string, []string, model.ProjectGateCommands, string, gates.TestScope) ([]model.CompletionGateResult, error) {
		t.Fatal("format-only policy expanded to non-format gates")
		return nil, nil
	}
	results, err := s.executeTaskFinalizeGatesWithSnapshot(context.Background(), "example", s.Config.Projects["example"], []string{model.WorkflowGateFormat}, nil, gates.FullTestScope())
	if err != nil || len(results) != 1 || results[0].ID != model.WorkflowGateFormat || results[0].ExitCode != 0 {
		t.Fatalf("format-only results=%#v err=%v", results, err)
	}
}
