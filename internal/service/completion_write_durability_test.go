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
)

func TestRunWriteCompletionRejectsOwnershipAndTaskHashMismatch(t *testing.T) {
	t.Run("ownership", func(t *testing.T) {
		s, task, run, _ := dispatchedRun(t, "receipt-owner")
		dir := t.TempDir()
		revision, err := s.Hub.RemoteRevision(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Hub.Transact(context.Background(), revision, "test: forge run owner", func(worktree string) ([]string, error) {
			var current model.Run
			if err := readWorktreeJSON(worktree, s.runPath(run.ProjectID, run.ID), &current); err != nil {
				return nil, err
			}
			current.GatewayID = "other_gateway"
			path := filepath.Join(worktree, filepath.FromSlash(s.runPath(run.ProjectID, run.ID)))
			return []string{s.runPath(run.ProjectID, run.ID)}, fsutil.WriteJSONAtomic(path, current, 0o600)
		}); err != nil {
			t.Fatal(err)
		}
		input := completionInput(t, dir, validCompletion(task, run))
		if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
			RunID:          run.ID,
			CompletionFile: input,
		}); err == nil || !strings.Contains(err.Error(), "assigned") {
			t.Fatalf("run ownership mismatch was not rejected: %v", err)
		}
	})

	t.Run("task-hash", func(t *testing.T) {
		s, task, run, _ := dispatchedRun(t, "receipt-task-hash")
		dir := t.TempDir()
		revision, err := s.Hub.RemoteRevision(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Hub.Transact(context.Background(), revision, "test: forge run task hash", func(worktree string) ([]string, error) {
			var current model.Run
			if err := readWorktreeJSON(worktree, s.runPath(run.ProjectID, run.ID), &current); err != nil {
				return nil, err
			}
			current.TaskSHA256 = strings.Repeat("b", 64)
			path := filepath.Join(worktree, filepath.FromSlash(s.runPath(run.ProjectID, run.ID)))
			return []string{s.runPath(run.ProjectID, run.ID)}, fsutil.WriteJSONAtomic(path, current, 0o600)
		}); err != nil {
			t.Fatal(err)
		}
		input := completionInput(t, dir, validCompletion(task, run))
		if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
			RunID:          run.ID,
			CompletionFile: input,
		}); err == nil || !strings.Contains(err.Error(), "identity") {
			t.Fatalf("run task hash mismatch was not rejected: %v", err)
		}
	})
}

func TestRunWriteCompletionRejectsInputSymlinkNonFiniteAndOversize(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "receipt-input-bounds")
	dir := t.TempDir()
	goodInput := completionInput(t, dir, validCompletion(task, run))
	inputLink := filepath.Join(dir, "input-link.json")
	if err := os.Symlink(goodInput, inputLink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: inputLink,
	}); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("completion input symlink was not rejected: %v", err)
	}

	nonFinite := filepath.Join(dir, "nonfinite.json")
	nonFiniteJSON := `{"schema_version":1,"run_id":"` + run.ID + `","task_sha256":"` + task.SHA256 + `","status":"needs_gpt_revision","summary":NaN,"gate_results":[],"acceptance_coverage":["AC1"],"deviations":[],"remaining_risks":[]}`
	if err := os.WriteFile(nonFinite, []byte(nonFiniteJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: nonFinite,
	}); err == nil {
		t.Fatal("non-finite completion value was accepted")
	}

	s.Config.MaxReadBytes = 128
	oversize := filepath.Join(dir, "oversize.json")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", 129)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: oversize,
	}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized completion was not rejected: %v", err)
	}
}

func TestRunWriteCompletionFailsClosedOnDirectoryDurabilityErrors(t *testing.T) {
	tests := []struct {
		name      string
		openErr   error
		syncErr   error
		closeErr  error
		want      string
		wantSync  int
		wantClose int
	}{
		{name: "open", openErr: errors.New("open failed"), want: "open completion directory", wantSync: 0, wantClose: 0},
		{name: "sync", syncErr: errors.New("sync failed"), want: "sync completion directory", wantSync: 1, wantClose: 1},
		{name: "close", closeErr: errors.New("close failed"), want: "close completion directory", wantSync: 1, wantClose: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, task, run, _ := dispatchedRun(t, "receipt-durability-"+test.name)
			dir := t.TempDir()
			input := completionInput(t, dir, validCompletion(task, run))
			fake := &fakeCompletionDirectory{
				syncErr:  test.syncErr,
				closeErr: test.closeErr,
			}
			previous := completionOpenDirectory
			completionOpenDirectory = func(string) (completionDirectory, error) {
				if test.openErr != nil {
					return nil, test.openErr
				}
				return fake, nil
			}
			t.Cleanup(func() { completionOpenDirectory = previous })
			if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
				RunID:          run.ID,
				CompletionFile: input,
			}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("directory durability failure was not returned: %v", err)
			}
			installed, err := os.ReadFile(run.CompletionPath)
			if err != nil {
				t.Fatalf("exclusive receipt was not installed before durability failure: %v", err)
			}
			canonical, err := model.CompletionJSON(validCompletion(task, run))
			if err != nil {
				t.Fatal(err)
			}
			if string(installed) != string(append(canonical, '\n')) {
				t.Fatal("durability failure changed the installed receipt content")
			}
			if fake.syncCalls != test.wantSync || fake.closeCalls != test.wantClose {
				t.Fatalf("unexpected directory calls: sync=%d close=%d", fake.syncCalls, fake.closeCalls)
			}
		})
	}
}
