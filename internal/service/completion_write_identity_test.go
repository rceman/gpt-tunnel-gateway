package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
	if _, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: input,
	}); err != nil {
		t.Fatal(err)
	}
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(context.Background(), FinalizeInput{
		RunID: run.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err != nil {
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
	result, err := s.RunWriteCompletion(context.Background(), CompletionWriteInput{
		RunID:          run.ID,
		CompletionFile: input,
	})
	if err != nil {
		t.Fatalf("canonical completion writer rejected legacy task hash: %v", err)
	}
	if result.Status != "WRITTEN" || result.TaskID != task.ID || result.RunID != run.ID {
		t.Fatalf("unexpected completion write result: %#v", result)
	}
}
