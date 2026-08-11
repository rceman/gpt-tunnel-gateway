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
	SchemaVersion int       `json:"schema_version"`
	ProjectID     string    `json:"project_id"`
	TrainID       string    `json:"train_id"`
	WorktreePath  string    `json:"worktree_path"`
	AgentID       string    `json:"agent_id"`
	SessionKey    string    `json:"session_key"`
	RunID         string    `json:"run_id"`
	StartedAt     time.Time `json:"started_at"`
}

type StartResult struct {
	Record  model.TrainV2StartRecord `json:"record"`
	Run     model.Run                `json:"run"`
	Runtime RuntimeBinding           `json:"runtime"`
}

const runtimeSchemaVersion = 1

func expectedWorktreePath(stateDir, projectID, trainID string) string {
	return filepath.Join(stateDir, "train-worktrees", projectID, trainID)
}

func runtimePath(stateDir, projectID, trainID string) string {
	return filepath.Join(stateDir, "train-runtime", projectID, trainID+".json")
}

func validateRuntimeBinding(v RuntimeBinding, stateDir string) error {
	if v.SchemaVersion != runtimeSchemaVersion || model.ValidateProjectIdentifier(v.ProjectID) != nil || v.WorktreePath != expectedWorktreePath(stateDir, v.ProjectID, v.TrainID) || model.ValidateObjectIdentifier(v.AgentID) != nil || v.SessionKey == "" || strings.ContainsAny(v.SessionKey, "\x00\r\n") || model.ValidateCanonicalRunID(v.RunID) != nil || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid local train runtime binding")
	}
	if _, _, err := model.ParseTrainV2ID(v.TrainID); err != nil {
		return fmt.Errorf("invalid local train runtime train ID")
	}
	return nil
}

func readRuntime(stateDir, projectID, trainID string) (RuntimeBinding, error) {
	var binding RuntimeBinding
	if err := fsutil.ReadJSONBounded(runtimePath(stateDir, projectID, trainID), 1<<20, &binding); err != nil {
		return RuntimeBinding{}, err
	}
	if err := validateRuntimeBinding(binding, stateDir); err != nil {
		return RuntimeBinding{}, err
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
	if err := model.ValidateTrainV2(deps.Train); err != nil || deps.Train.ProjectID != in.ProjectID || deps.Train.ID != in.TrainID || deps.Train.Status != model.TrainV2Planned || len(deps.Train.Items) == 0 {
		return StartResult{}, fmt.Errorf("train v2 is not startable")
	}
	if binding, err := readRuntime(deps.StateDir, in.ProjectID, in.TrainID); err == nil {
		record, recordErr := readStartRecord(ctx, deps.Hub, in.ProjectID, in.TrainID)
		if recordErr != nil {
			return StartResult{}, fmt.Errorf("local train runtime has no durable start record: %w", recordErr)
		}
		run, runErr := readRun(ctx, deps.Hub, in.ProjectID, binding.RunID)
		if runErr != nil {
			return StartResult{}, runErr
		}
		return StartResult{Record: record, Run: run, Runtime: binding}, nil
	} else if !os.IsNotExist(err) {
		return StartResult{}, err
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
	worktreePath := expectedWorktreePath(deps.StateDir, in.ProjectID, in.TrainID)
	if err := deps.Git.CreateTrainWorktree(ctx, deps.ProjectConfig, deps.StateDir, in.ProjectID, in.TrainID, laneBranch, base); err != nil {
		return StartResult{}, err
	}
	createdWorktree := true
	defer func() {
		if createdWorktree {
			_ = deps.Git.RemoveTrainWorktree(context.Background(), deps.ProjectConfig, deps.StateDir, in.ProjectID, in.TrainID)
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
	runtime := RuntimeBinding{SchemaVersion: runtimeSchemaVersion, ProjectID: in.ProjectID, TrainID: in.TrainID, WorktreePath: worktreePath, AgentID: in.ResolvedAgentID, SessionKey: in.SessionKey, StartedAt: now}
	if err := model.ValidateObjectIdentifier(runtime.AgentID); err != nil {
		return StartResult{}, err
	}
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		return StartResult{}, err
	}
	removeRuntime := true
	defer func() {
		if removeRuntime {
			_ = os.Remove(runtimePath(deps.StateDir, in.ProjectID, in.TrainID))
		}
	}()
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = deps.Hub.RemoteRevision(ctx)
		if err != nil {
			return StartResult{}, err
		}
	}
	startRoot := projectRoot(in.ProjectID) + "/train-v2-starts"
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
		if err := hub.WriteJSON(w, startRoot+"/"+in.TrainID+".json", record); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, runPath(in.ProjectID, runID), run); err != nil {
			return nil, err
		}
		runtime.RunID = runID
		return []string{startRoot + "/" + in.TrainID + ".json", runPath(in.ProjectID, runID)}, nil
	})
	if err != nil {
		return StartResult{}, err
	}
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
		return StartResult{}, err
	}
	message := "Read train item " + item.TaskID + " and execute it in train " + in.TrainID + ". Use the server-owned train worktree and report completion through the Train item lifecycle."
	dispatch, dispatchErr := deps.Airelay.Prompt(ctx, in.SessionKey, message)
	if dispatchErr != nil {
		return StartResult{}, fmt.Errorf("train agent dispatch failed: %w", dispatchErr)
	}
	code := dispatch.ExitCode
	run.Status, run.DispatchMessage, run.DispatchExitCode, run.DispatchStdout, run.DispatchStderr = "dispatched", message, &code, dispatch.Stdout, dispatch.Stderr
	dispatchedAt := dispatch.FinishedAt
	if dispatchedAt.IsZero() {
		dispatchedAt = now
	}
	run.DispatchedAt = &dispatchedAt
	if _, err := deps.Hub.Transact(ctx, tx.After, "gateway: dispatch train v2 first item "+in.TrainID, func(w string) ([]string, error) {
		var current model.Run
		if err := readWorktreeJSON(w, runPath(in.ProjectID, runID), &current); err != nil {
			return nil, err
		}
		current.Status, current.DispatchMessage, current.DispatchExitCode, current.DispatchStdout, current.DispatchStderr, current.DispatchedAt = run.Status, run.DispatchMessage, run.DispatchExitCode, run.DispatchStdout, run.DispatchStderr, run.DispatchedAt
		if err := model.ValidateRun(current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, runPath(in.ProjectID, runID), current); err != nil {
			return nil, err
		}
		return []string{runPath(in.ProjectID, runID)}, nil
	}); err != nil {
		return StartResult{}, err
	}
	removeRuntime, createdWorktree = false, false
	runtime.RunID = runID
	return StartResult{Record: record, Run: run, Runtime: runtime}, nil
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
