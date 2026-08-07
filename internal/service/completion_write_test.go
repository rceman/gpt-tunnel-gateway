package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

type fakeCompletionDirectory struct {
	syncErr    error
	closeErr   error
	syncCalls  int
	closeCalls int
}

func (f *fakeCompletionDirectory) Sync() error {
	f.syncCalls++
	return f.syncErr
}

func (f *fakeCompletionDirectory) Close() error {
	f.closeCalls++
	return f.closeErr
}

func completionInput(t *testing.T, dir string, completion model.Completion) string {
	t.Helper()
	data, err := model.CompletionJSON(completion)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, "completion-input.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validCompletion(task model.Task, run model.Run) model.Completion {
	return model.CanonicalCompletion(model.Completion{
		SchemaVersion:      model.SchemaVersion,
		RunID:              run.ID,
		TaskSHA256:         task.SHA256,
		Status:             "needs_gpt_revision",
		Summary:            "bounded canonical receipt",
		AcceptanceCoverage: []string{"AC1"},
		Deviations:         []string{"test-only bounded deviation"},
		RemainingRisks:     []string{},
		GateResults:        []model.CompletionGateResult{},
	})
}

func TestRunFinalizePersistsAndReadsAgentFeedback(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "receipt-agent-feedback")
	feedback := model.AgentFeedback{
		Summary:      "The run completed with one reusable improvement.",
		Friction:     []string{"The slow gate has limited progress visibility."},
		Improvements: []string{"Expose bounded progress updates."},
		ToolCandidates: []model.AgentFeedbackToolCandidate{{
			Problem:        "Repeatedly waiting without status.",
			ProposedTool:   "gate_progress",
			ExpectedReuse:  "recurring",
			ExpectedSaving: "Reduce idle waiting.",
			SafetyBoundary: "Read-only and bounded; never retries execution.",
		}},
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "agent-feedback.txt"), []byte("feedback\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "agent-feedback.txt")
	testutil.Git(t, project.Root, "commit", "-m", "agent feedback proof")
	testutil.Git(t, project.Root, "push", "origin", run.Branch)
	completion := validCompletion(task, run)
	completion.AgentFeedback = &feedback
	input := completionInput(t, t.TempDir(), completion)
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input}); err != nil {
		t.Fatal(err)
	}
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(context.Background(), FinalizeInput{RunID: run.ID, WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err != nil {
		t.Fatal(err)
	}
	read, err := s.RunReport(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.AgentFeedback == nil || read.AgentFeedback.ToolCandidates[0].ProposedTool != "gate_progress" {
		t.Fatalf("agent feedback was not preserved through finalization/read: %#v", read.AgentFeedback)
	}
}

func legacyTaskHashForTest(task model.Task) string {
	task.SHA256 = ""
	legacy := struct {
		SchemaVersion      int       `json:"schema_version"`
		ID                 string    `json:"id"`
		SHA256             string    `json:"sha256"`
		ProjectID          string    `json:"project_id"`
		Title              string    `json:"title"`
		Objective          string    `json:"objective"`
		Branch             string    `json:"branch"`
		BaseRevision       string    `json:"base_revision"`
		AcceptanceCriteria []string  `json:"acceptance_criteria"`
		Constraints        []string  `json:"constraints"`
		RequiredGates      []string  `json:"required_gates,omitempty"`
		Status             string    `json:"status"`
		Supersedes         string    `json:"supersedes,omitempty"`
		CreatedBy          string    `json:"created_by"`
		CreatedAt          time.Time `json:"created_at"`
	}{task.SchemaVersion, task.ID, task.SHA256, task.ProjectID, task.Title, task.Objective, task.Branch, task.BaseRevision, task.AcceptanceCriteria, task.Constraints, task.RequiredGates, task.Status, task.Supersedes, task.CreatedBy, task.CreatedAt}
	data, err := json.Marshal(legacy)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestRunWriteCompletionAcceptsHistoricalTaskHashProjection(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "receipt-historical-task")
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	task.OperationClass = ""
	task.WorkflowPolicyRevision = 0
	task.EffectiveCIField = ""
	task.EffectiveCIMode = ""
	task.WaitForCI = false
	task.CIBlocking = false
	task.AgentMayWait = false
	task.SHA256 = legacyTaskHashForTest(task)
	run.TaskSHA256 = task.SHA256
	_, err = s.Hub.Transact(context.Background(), revision, "test: install historical task hash", func(worktree string) ([]string, error) {
		taskPath := filepath.Join(worktree, filepath.FromSlash(s.taskPath(task.ProjectID, task.ID)))
		runPath := filepath.Join(worktree, filepath.FromSlash(s.runPath(run.ProjectID, run.ID)))
		if err := fsutil.WriteJSONAtomic(taskPath, task, 0o600); err != nil {
			return nil, err
		}
		var currentRun model.Run
		if err := readWorktreeJSON(worktree, s.runPath(run.ProjectID, run.ID), &currentRun); err != nil {
			return nil, err
		}
		currentRun.TaskSHA256 = task.SHA256
		if err := fsutil.WriteJSONAtomic(runPath, currentRun, 0o600); err != nil {
			return nil, err
		}
		return []string{s.taskPath(task.ProjectID, task.ID), s.runPath(run.ProjectID, run.ID)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	input := completionInput(t, t.TempDir(), validCompletion(task, run))
	result, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input})
	if err != nil {
		t.Fatalf("canonical completion writer rejected legacy task hash: %v", err)
	}
	if result.Status != "WRITTEN" || result.TaskID != task.ID || result.RunID != run.ID {
		t.Fatalf("unexpected completion write result: %#v", result)
	}
}

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

	result, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input})
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

	again, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input})
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
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: badInput}); err == nil {
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
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: goodInput}); err == nil || !strings.Contains(err.Error(), "completion") {
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
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: goodInput}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(run.CompletionPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := validCompletion(task, run)
	changed.Summary = "different canonical receipt"
	changedInput := completionInput(t, dir, changed)
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: changedInput}); err == nil || !strings.Contains(err.Error(), "different content") {
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
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: invalidInput}); err == nil {
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
		if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input}); err == nil || !strings.Contains(err.Error(), "canonical Run-specific path") {
			t.Fatalf("noncanonical completion path was not rejected: %v", err)
		}
	}
}

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
		if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input}); err == nil || !strings.Contains(err.Error(), "assigned") {
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
		if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input}); err == nil || !strings.Contains(err.Error(), "identity") {
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
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: inputLink}); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("completion input symlink was not rejected: %v", err)
	}

	nonFinite := filepath.Join(dir, "nonfinite.json")
	nonFiniteJSON := `{"schema_version":1,"run_id":"` + run.ID + `","task_sha256":"` + task.SHA256 + `","status":"needs_gpt_revision","summary":NaN,"gate_results":[],"acceptance_coverage":["AC1"],"deviations":[],"remaining_risks":[]}`
	if err := os.WriteFile(nonFinite, []byte(nonFiniteJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: nonFinite}); err == nil {
		t.Fatal("non-finite completion value was accepted")
	}

	s.Config.MaxReadBytes = 128
	oversize := filepath.Join(dir, "oversize.json")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", 129)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: oversize}); err == nil || !strings.Contains(err.Error(), "exceeds") {
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
			fake := &fakeCompletionDirectory{syncErr: test.syncErr, closeErr: test.closeErr}
			previous := completionOpenDirectory
			completionOpenDirectory = func(string) (completionDirectory, error) {
				if test.openErr != nil {
					return nil, test.openErr
				}
				return fake, nil
			}
			t.Cleanup(func() { completionOpenDirectory = previous })
			if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{RunID: run.ID, CompletionFile: input}); err == nil || !strings.Contains(err.Error(), test.want) {
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
