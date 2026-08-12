package train

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type StartInput struct {
	ProjectID           string
	TrainID             string
	StartedBy           string
	AgentID             string
	RequestedReasoning  string
	ResolvedReasoning   string
	ResolvedAgentID     string
	SessionKey          string
	AgentFallback       bool
	AgentFallbackReason string
	ExpectedHubRevision string
}

type StartDependencies struct {
	Hub           hub.Store
	Git           gitx.Runner
	Airelay       airelay.Client
	ProjectConfig config.ProjectConfig
	Project       model.Project
	Policy        model.ProjectWorkflowPolicy
	Train         model.TrainV2
	GatewayID     string
	StateDir      string
	Now           func() time.Time
}

type RuntimeBinding struct {
	SchemaVersion   int       `json:"schema_version"`
	ProjectID       string    `json:"project_id"`
	TrainID         string    `json:"train_id"`
	WorktreePath    string    `json:"worktree_path"`
	AgentID         string    `json:"agent_id"`
	SessionKey      string    `json:"session_key"`
	RunID           string    `json:"run_id"`
	RestartRequired bool      `json:"restart_required,omitempty"`
	StartedAt       time.Time `json:"started_at"`
}

type StartResult struct {
	Record  model.TrainV2StartRecord `json:"record"`
	Run     model.Run                `json:"run"`
	Runtime RuntimeBinding           `json:"runtime"`
}

type dispatchReceipt struct {
	RunID      string    `json:"run_id"`
	SessionKey string    `json:"session_key"`
	Message    string    `json:"message"`
	ExitCode   int       `json:"exit_code"`
	Stdout     string    `json:"stdout,omitempty"`
	Stderr     string    `json:"stderr,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

const runtimeSchemaVersion = 1

func ExpectedWorktreePath(stateDir, projectID, trainID string) string {
	return filepath.Join(stateDir, "train-worktrees", projectID, trainID)
}

func runtimePath(stateDir, projectID, trainID string) string {
	return filepath.Join(stateDir, "train-runtime", projectID, trainID+".json")
}

func dispatchReceiptPath(stateDir, projectID, trainID string) string {
	return runtimePath(stateDir, projectID, trainID) + ".dispatch.json"
}

// RuntimePath exposes the Gateway-local binding location to service adapters.
func RuntimePath(stateDir, projectID, trainID string) string {
	return runtimePath(stateDir, projectID, trainID)
}

func ValidateRuntimeBindingShape(v RuntimeBinding) error {
	if v.SchemaVersion != runtimeSchemaVersion || model.ValidateProjectIdentifier(v.ProjectID) != nil || v.WorktreePath == "" || model.ValidateObjectIdentifier(v.AgentID) != nil || v.SessionKey == "" || strings.ContainsAny(v.SessionKey, "\x00\r\n") || model.ValidateCanonicalRunID(v.RunID) != nil || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid local train runtime binding")
	}
	if _, _, err := model.ParseTrainV2ID(v.TrainID); err != nil {
		return fmt.Errorf("invalid local train runtime train ID")
	}
	return nil
}

func ValidateRuntimeBinding(v RuntimeBinding, stateDir string) error {
	if err := ValidateRuntimeBindingShape(v); err != nil {
		return err
	}
	if v.WorktreePath != ExpectedWorktreePath(stateDir, v.ProjectID, v.TrainID) {
		return fmt.Errorf("invalid local train runtime worktree path")
	}
	return nil
}

func ReadRuntime(stateDir, projectID, trainID string) (RuntimeBinding, error) {
	var binding RuntimeBinding
	if err := fsutil.ReadJSONBounded(runtimePath(stateDir, projectID, trainID), 1<<20, &binding); err != nil {
		return RuntimeBinding{}, err
	}
	if err := ValidateRuntimeBinding(binding, stateDir); err != nil {
		return RuntimeBinding{}, err
	}
	return binding, nil
}

// RetireRuntimeForRestart preserves the server-owned lane binding while
// retiring the current execution generation.  The next Start must create a
// new Run from the retained refreshed-target checkout; it must not resume the
// old Run or its dispatch receipt.
func RetireRuntimeForRestart(stateDir, projectID, trainID, expectedRunID string) (RuntimeBinding, error) {
	binding, err := ReadRuntime(stateDir, projectID, trainID)
	if err != nil {
		return RuntimeBinding{}, err
	}
	if expectedRunID != "" && binding.RunID != expectedRunID {
		return RuntimeBinding{}, fmt.Errorf("Train runtime generation does not match the reconciled Run")
	}
	binding.RestartRequired = true
	data, err := json.Marshal(binding)
	if err != nil {
		return RuntimeBinding{}, err
	}
	if err := fsutil.WriteFileAtomic(runtimePath(stateDir, projectID, trainID), data, 0o600); err != nil {
		return RuntimeBinding{}, err
	}
	if err := os.Remove(dispatchReceiptPath(stateDir, projectID, trainID)); err != nil && !os.IsNotExist(err) {
		return RuntimeBinding{}, fmt.Errorf("retire stale Train dispatch receipt: %w", err)
	}
	return binding, nil
}

func Start(ctx context.Context, in StartInput, deps StartDependencies) (StartResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return StartResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return StartResult{}, err
	}
	if err := model.ValidateTrainV2(deps.Train); err != nil || deps.Train.ProjectID != in.ProjectID || deps.Train.ID != in.TrainID || len(deps.Train.Items) == 0 {
		return StartResult{}, fmt.Errorf("train v2 is not startable")
	}
	reuseWorktree := false
	if binding, err := ReadRuntime(deps.StateDir, in.ProjectID, in.TrainID); err == nil {
		if binding.RestartRequired || deps.Train.Status == model.TrainV2Planned {
			if deps.Train.Status != model.TrainV2Planned {
				return StartResult{}, fmt.Errorf("Train restart marker requires a planned Train")
			}
			reuseWorktree = true
		} else {
			if deps.Train.Status != model.TrainV2Running && deps.Train.Status != model.TrainV2Paused {
				return StartResult{}, fmt.Errorf("existing Train runtime belongs to a non-operational execution generation")
			}
			if binding.AgentID != in.ResolvedAgentID || binding.SessionKey != in.SessionKey {
				return StartResult{}, fmt.Errorf("Train already has a different Agent/session binding")
			}
			record, recordErr := readStartRecord(ctx, deps.Hub, in.ProjectID, in.TrainID)
			if recordErr != nil {
				return StartResult{}, fmt.Errorf("local train runtime has no durable start record: %w", recordErr)
			}
			run, runErr := readRun(ctx, deps.Hub, in.ProjectID, binding.RunID)
			if runErr != nil {
				return StartResult{}, runErr
			}
			if run.Status == "created" || run.Status == "dispatching" {
				if err := resumeDispatch(ctx, deps, run, binding.SessionKey); err != nil {
					return StartResult{}, err
				}
				run, runErr = readRun(ctx, deps.Hub, in.ProjectID, binding.RunID)
				if runErr != nil {
					return StartResult{}, runErr
				}
			}
			return StartResult{Record: record, Run: run, Runtime: binding}, nil
		}
	} else if !os.IsNotExist(err) {
		return StartResult{}, err
	}
	if deps.Train.Status != model.TrainV2Planned {
		// The Hub start/run transaction may have committed before the local
		// binding write completed. Reconstruct the binding from those durable
		// records instead of leaving a running Train permanently unrecoverable.
		recovered, recoverErr := recoverMissingRuntime(ctx, deps)
		if recoverErr == nil {
			return recovered, nil
		}
		return StartResult{}, fmt.Errorf("train v2 is already started without recoverable local runtime")
	}
	if in.StartedBy == "" || strings.ContainsAny(in.StartedBy, "\x00\r\n") || in.SessionKey == "" || in.ResolvedAgentID == "" {
		return StartResult{}, fmt.Errorf("train start identity is incomplete")
	}
	status, err := deps.Git.WorktreeStatus(ctx, deps.ProjectConfig)
	if err != nil {
		return StartResult{}, err
	}
	if !status.Clean {
		return StartResult{}, fmt.Errorf("project worktree is dirty")
	}
	if err := deps.Git.Refresh(ctx, deps.ProjectConfig); err != nil {
		return StartResult{}, fmt.Errorf("refresh integration branch: %w", err)
	}
	integrationBranch := deps.Policy.IntegrationBranch
	if integrationBranch == "" {
		integrationBranch = deps.Project.DefaultBranch
	}
	base, exists, err := deps.Git.MirrorBranchHead(ctx, deps.ProjectConfig, integrationBranch)
	if err != nil || !exists || model.ValidateCommitSHA(base) != nil {
		return StartResult{}, fmt.Errorf("integration branch %q does not resolve to an exact commit", integrationBranch)
	}
	laneBranch := "train/" + deps.Train.ID
	worktreePath := ExpectedWorktreePath(deps.StateDir, in.ProjectID, in.TrainID)
	createdWorktree := false
	if reuseWorktree {
		lane := deps.ProjectConfig
		lane.Root = worktreePath
		head, branch, clean, headErr := deps.Git.CurrentHead(ctx, lane)
		if headErr != nil || !clean || branch != laneBranch || head != base {
			return StartResult{}, fmt.Errorf("retired Train lane is not clean at the refreshed target")
		}
	} else {
		if err := deps.Git.CreateTrainWorktree(ctx, deps.ProjectConfig, deps.StateDir, in.ProjectID, in.TrainID, laneBranch, base); err != nil {
			return StartResult{}, err
		}
		createdWorktree = true
	}
	if reuseWorktree {
		if err := os.Remove(dispatchReceiptPath(deps.StateDir, in.ProjectID, in.TrainID)); err != nil && !os.IsNotExist(err) {
			return StartResult{}, fmt.Errorf("remove stale Train dispatch receipt: %w", err)
		}
	}
	defer func() {
		if createdWorktree {
			_ = deps.Git.RemoveTrainWorktree(context.Background(), deps.ProjectConfig, deps.StateDir, in.ProjectID, in.TrainID)
			_ = deps.Git.DeleteTrainBranch(context.Background(), deps.ProjectConfig, laneBranch, base)
		}
	}()
	now := time.Now().UTC()
	if deps.Now != nil {
		now = deps.Now().UTC()
	}
	item := deps.Train.Items[0]
	var runID string
	var record model.TrainV2StartRecord
	var run model.Run
	runtime := RuntimeBinding{SchemaVersion: runtimeSchemaVersion, ProjectID: in.ProjectID, TrainID: in.TrainID, WorktreePath: worktreePath, AgentID: in.ResolvedAgentID, SessionKey: in.SessionKey, RestartRequired: false, StartedAt: now}
	if err := model.ValidateObjectIdentifier(runtime.AgentID); err != nil {
		return StartResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = deps.Hub.RemoteRevision(ctx)
		if err != nil {
			return StartResult{}, err
		}
	}
	startRoot := projectRoot(in.ProjectID) + "/train-v2-starts"
	integrationReceiptPath := projectRoot(in.ProjectID) + "/trains-v2/" + in.TrainID + ".integration.json"
	tx, err := deps.Hub.Transact(ctx, expected, "gateway: start train v2 "+in.TrainID, func(w string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(w, trainPath(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != deps.Train.Revision || latest.Status != model.TrainV2Planned || len(latest.Items) == 0 || latest.Items[0].TaskID != item.TaskID || latest.Items[0].TaskRevision != item.TaskRevision || latest.Items[0].TaskRevisionSHA256 != item.TaskRevisionSHA256 {
			return nil, fmt.Errorf("train v2 changed before start")
		}
		runID, err = nextRunID(w, projectRoot(in.ProjectID)+"/runs", item.TaskID)
		if err != nil {
			return nil, err
		}
		completionPath := filepath.Join(deps.StateDir, "runs", runID, "completion.json")
		run = model.Run{SchemaVersion: model.SchemaVersion, ID: runID, TaskID: item.TaskID, TaskSHA256: item.TaskRevisionSHA256, ProjectID: in.ProjectID, GatewayID: deps.GatewayID, SessionKey: in.SessionKey, AgentID: in.ResolvedAgentID, RequestedReasoning: in.RequestedReasoning, ResolvedReasoning: in.ResolvedReasoning, AgentFallback: in.AgentFallback, AgentFallbackReason: in.AgentFallbackReason, Branch: laneBranch, TrainID: in.TrainID, LaneBranch: laneBranch, BaseRevision: base, Status: "created", CompletionPath: completionPath, CreatedAt: now}
		if err := model.ValidateRun(run); err != nil {
			return nil, err
		}
		record = model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: in.ProjectID, TrainID: in.TrainID, Status: model.TrainV2StartActive, IntegrationBranch: integrationBranch, BaseRevision: base, LaneBranch: laneBranch, RunID: runID, CurrentTaskID: item.TaskID, CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256, StartedAt: now}
		if err := model.ValidateTrainV2StartRecord(record); err != nil {
			return nil, err
		}
		latest.Status = model.TrainV2Running
		latest.Items[0].Status = model.TrainV2ItemRunning
		latest.Items[0].RunID = runID
		latest.Items[0].AgentID = in.ResolvedAgentID
		latest.Items[0].StartHead = base
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, startRoot+"/"+in.TrainID+".json", record); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, runPath(in.ProjectID, runID), run); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, trainPath(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		runtime.RunID = runID
		paths := []string{startRoot + "/" + in.TrainID + ".json", runPath(in.ProjectID, runID), trainPath(in.ProjectID, in.TrainID)}
		if reuseWorktree {
			if _, statErr := os.Lstat(filepath.Join(w, filepath.FromSlash(integrationReceiptPath))); statErr == nil {
				if err := os.Remove(filepath.Join(w, filepath.FromSlash(integrationReceiptPath))); err != nil {
					return nil, fmt.Errorf("retire prior Train reconciliation receipt: %w", err)
				}
				paths = append(paths, integrationReceiptPath)
			} else if !os.IsNotExist(statErr) {
				return nil, statErr
			}
		}
		return paths, nil
	})
	if err != nil {
		return StartResult{}, err
	}
	// Keep the server-owned lane and durable start/run in place if the local
	// binding write fails. A later Start call can reconstruct the binding from
	// the committed records; deleting the lane here would leave an active
	// Train pointing at no usable runtime.
	createdWorktree = false
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		return StartResult{}, err
	}
	// From this point the durable start/run and local binding are recoverable;
	// a prompt or dispatch failure must leave them intact for retry.
	dispatchedRun, err := dispatchRun(ctx, deps, run, in.SessionKey, tx.After, now)
	if err != nil {
		return StartResult{}, err
	}
	runtime.RunID = runID
	return StartResult{Record: record, Run: dispatchedRun, Runtime: runtime}, nil
}

func recoverMissingRuntime(ctx context.Context, deps StartDependencies) (StartResult, error) {
	record, err := readStartRecord(ctx, deps.Hub, deps.Project.ID, deps.Train.ID)
	if err != nil || record.Status != model.TrainV2StartActive || record.RunID == "" {
		return StartResult{}, fmt.Errorf("durable Train start record is unavailable")
	}
	run, err := readRun(ctx, deps.Hub, deps.Project.ID, record.RunID)
	if err != nil || run.TrainID != deps.Train.ID || run.ProjectID != deps.Project.ID || run.Status == "" {
		return StartResult{}, fmt.Errorf("durable Train Run is unavailable")
	}
	path := ExpectedWorktreePath(deps.StateDir, deps.Project.ID, deps.Train.ID)
	if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
		return StartResult{}, fmt.Errorf("server-owned Train worktree is unavailable")
	}
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     deps.Project.ID,
		TrainID:       deps.Train.ID,
		WorktreePath:  path,
		AgentID:       run.AgentID,
		SessionKey:    run.SessionKey,
		RunID:         run.ID,
		StartedAt:     record.StartedAt,
	}
	if err := ValidateRuntimeBinding(binding, deps.StateDir); err != nil {
		return StartResult{}, err
	}
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, deps.Project.ID, deps.Train.ID), binding, 0o600); err != nil {
		return StartResult{}, err
	}
	if run.Status == "created" || run.Status == "dispatching" {
		if err := resumeDispatch(ctx, deps, run, binding.SessionKey); err != nil {
			return StartResult{}, err
		}
		run, err = readRun(ctx, deps.Hub, deps.Project.ID, run.ID)
		if err != nil {
			return StartResult{}, err
		}
	}
	return StartResult{Record: record, Run: run, Runtime: binding}, nil
}

func resumeDispatch(ctx context.Context, deps StartDependencies, run model.Run, sessionKey string) error {
	_, err := dispatchRun(ctx, deps, run, sessionKey, "", time.Now().UTC())
	return err
}

func dispatchRun(ctx context.Context, deps StartDependencies, run model.Run, sessionKey, expected string, now time.Time) (model.Run, error) {
	receiptPath := dispatchReceiptPath(deps.StateDir, run.ProjectID, run.TrainID)
	var receipt dispatchReceipt
	if err := fsutil.ReadJSONBounded(receiptPath, 1<<20, &receipt); err != nil {
		if !os.IsNotExist(err) {
			return model.Run{}, fmt.Errorf("read durable Train dispatch receipt: %w", err)
		}
		message := "Resume train item " + run.TaskID + " in train " + run.TrainID + ". Use the existing server-owned Train worktree and report completion through the Train item lifecycle."
		dispatch, promptErr := deps.Airelay.Prompt(ctx, sessionKey, message)
		if promptErr != nil {
			return model.Run{}, fmt.Errorf("train agent dispatch retry failed: %w", promptErr)
		}
		finishedAt := dispatch.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = now
		}
		receipt = dispatchReceipt{RunID: run.ID, SessionKey: sessionKey, Message: message, ExitCode: dispatch.ExitCode, Stdout: dispatch.Stdout, Stderr: dispatch.Stderr, FinishedAt: finishedAt}
		if err := fsutil.WriteJSONAtomic(receiptPath, receipt, 0o600); err != nil {
			return model.Run{}, fmt.Errorf("persist durable Train dispatch receipt: %w", err)
		}
	}
	if receipt.RunID != run.ID || receipt.SessionKey != sessionKey || receipt.Message == "" || receipt.FinishedAt.IsZero() {
		return model.Run{}, fmt.Errorf("durable Train dispatch receipt does not match the Run")
	}
	code := receipt.ExitCode
	run.Status = "dispatched"
	run.DispatchMessage = receipt.Message
	run.DispatchExitCode = &code
	run.DispatchStdout = receipt.Stdout
	run.DispatchStderr = receipt.Stderr
	run.DispatchedAt = &receipt.FinishedAt
	if expected == "" {
		var err error
		expected, err = deps.Hub.RemoteRevision(ctx)
		if err != nil {
			return model.Run{}, err
		}
	}
	_, err := deps.Hub.Transact(ctx, expected, "gateway: recover train v2 dispatch "+run.TrainID, func(worktree string) ([]string, error) {
		var current model.Run
		if err := readWorktreeJSON(worktree, runPath(run.ProjectID, run.ID), &current); err != nil {
			return nil, err
		}
		if current.Status != "created" && current.Status != "dispatching" {
			return nil, fmt.Errorf("train Run changed before dispatch recovery")
		}
		if err := model.ValidateRun(run); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, runPath(run.ProjectID, run.ID), run); err != nil {
			return nil, err
		}
		return []string{runPath(run.ProjectID, run.ID)}, nil
	})
	if err == nil {
		_ = os.Remove(receiptPath)
	}
	return run, err
}

func projectRoot(projectID string) string { return hub.ProtocolRoot + "/projects/" + projectID }
func trainPath(projectID, trainID string) string {
	return projectRoot(projectID) + "/trains-v2/" + trainID + ".json"
}
func runPath(projectID, runID string) string {
	return projectRoot(projectID) + "/runs/" + runID + "/run.json"
}

func readStartRecord(ctx context.Context, store hub.Store, projectID, trainID string) (model.TrainV2StartRecord, error) {
	var record model.TrainV2StartRecord
	if err := store.ReadJSON(ctx, projectRoot(projectID)+"/train-v2-starts/"+trainID+".json", &record); err != nil {
		return record, err
	}
	if err := model.ValidateTrainV2StartRecord(record); err != nil {
		return record, err
	}
	return record, nil
}

func readRun(ctx context.Context, store hub.Store, projectID, runID string) (model.Run, error) {
	var run model.Run
	if err := store.ReadJSON(ctx, runPath(projectID, runID), &run); err != nil {
		return run, err
	}
	if err := model.ValidateRun(run); err != nil {
		return run, err
	}
	return run, nil
}

func nextRunID(worktree, root, taskID string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(worktree, filepath.FromSlash(root)))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	var next uint64 = 1
	for _, entry := range entries {
		if entry.IsDir() {
			if parent, n, e := model.ParseRunID(entry.Name()); e == nil && parent == taskID && n >= next {
				next = n + 1
			}
		}
	}
	return model.FormatRunID(taskID, next)
}

// NextRunID allocates the next canonical Run ID from the durable Hub tree.
// Service adapters use it when a finalized Train item advances in place.
func NextRunID(worktree, root, taskID string) (string, error) {
	return nextRunID(worktree, root, taskID)
}

func readWorktreeJSON(worktree, path string, out any) error {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("worktree JSON has trailing content")
	}
	return nil
}
