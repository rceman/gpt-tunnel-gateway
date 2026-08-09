package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestRunWriteCompletionUsesAuthoritativeTaskRunAndCompletionPath(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "receipt-authority")
	dir := t.TempDir()
	input := completionInput(t, dir, validCompletion(task, run))

	// A caller-selected local task/run tree is not an input to this operation.
	fakeRoot := filepath.Join(dir, ".gpt", "run", task.ID, "run-1")
	if err := os.MkdirAll(fakeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeRoot, "task.json"), []byte(`{"id":"forged"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "WRITTEN" || result.Path != run.CompletionPath || result.TaskID != task.ID {
		t.Fatalf("unexpected write result: %#v", result)
	}
	if _, err := os.Stat(run.CompletionPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fakeRoot, "completion.json")); !os.IsNotExist(err) {
		t.Fatalf("caller-selected local authority was used: %v", err)
	}

	again, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != "ALREADY_PRESENT" {
		t.Fatalf("identical receipt was not idempotent: %#v", again)
	}
}

func TestRunWriteCompletionRejectsForgedIdentityAndDestinationEscape(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "receipt-adversarial")
	dir := t.TempDir()
	bad := validCompletion(task, run)
	bad.TaskSHA256 = strings.Repeat("a", 64)
	badInput := completionInput(t, dir, bad)
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: badInput,
	}); err == nil {
		t.Fatal("forged completion task hash was accepted")
	}
	if _, err := os.Stat(run.CompletionPath); !os.IsNotExist(err) {
		t.Fatalf("forged completion created a file: %v", err)
	}

	outside := filepath.Join(dir, "outside.json")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, run.CompletionPath); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(run.CompletionPath)
	goodInput := completionInput(t, dir, validCompletion(task, run))
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: goodInput,
	}); err == nil || !strings.Contains(err.Error(), "completion") {
		t.Fatalf("completion symlink was not rejected: %v", err)
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "outside" {
		t.Fatalf("symlink target was modified: %q", content)
	}
}

func TestRunWriteCompletionRejectsConflictingContentAndInvalidJSON(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "receipt-conflict")
	dir := t.TempDir()
	goodInput := completionInput(t, dir, validCompletion(task, run))
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: goodInput,
	}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(run.CompletionPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := validCompletion(task, run)
	changed.Summary = "different canonical receipt"
	changedInput := completionInput(t, dir, changed)
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: changedInput,
	}); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("conflicting receipt was not rejected: %v", err)
	}
	current, err := os.ReadFile(run.CompletionPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("conflicting receipt changed the canonical file")
	}

	invalidInput := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalidInput, []byte(`{"schema_version":1,"run_id":"`+run.ID+`","run_id":"duplicate"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: invalidInput,
	}); err == nil {
		t.Fatal("duplicate completion fields were accepted")
	}

	var decoded model.Completion
	data, err := json.Marshal(validCompletion(task, run))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RunID != run.ID {
		t.Fatal("canonical completion identity changed during JSON round trip")
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "unrelated.json"), map[string]string{"status": "untouched"}, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunWriteCompletionRejectsCompletionPathOutsideState(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "receipt-escape")
	dir := t.TempDir()
	input := completionInput(t, dir, validCompletion(task, run))
	for _, destination := range []string{
		filepath.Join(dir, "external", "completion.json"),
		filepath.Join(s.Config.StateDir, "runs", "sibling-run", "completion.json"),
	} {
		hubRevision, err := s.Hub.RemoteRevision(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Hub.Transact(context.Background(), hubRevision, "test: forge noncanonical completion path", func(worktree string) ([]string, error) {
			path := filepath.Join(worktree, filepath.FromSlash(s.runPath(run.ProjectID, run.ID)))
			var current model.Run
			if err := readWorktreeJSON(worktree, s.runPath(run.ProjectID, run.ID), &current); err != nil {
				return nil, err
			}
			current.CompletionPath = destination
			return []string{s.runPath(run.ProjectID, run.ID)}, fsutil.WriteJSONAtomic(path, current, 0o600)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
			RunID:          run.ID,
			CompletionFile: input,
		}); err == nil || !strings.Contains(err.Error(), "canonical Run-specific path") {
			t.Fatalf("noncanonical completion path was not rejected: %v", err)
		}
	}
}
