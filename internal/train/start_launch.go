package train

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
			if run.Status == "created" || run.Status == "dispatching" || run.Status == "dispatched" {
				if err := resumeDispatch(ctx, deps, run, binding.SessionKey); err != nil {
					return StartResult{}, err
				}
				run, runErr = readRun(ctx, deps.Hub, in.ProjectID, binding.RunID)
				if runErr != nil {
					return StartResult{}, runErr
				}
			}
			return StartResult{
				Record:  record,
				Run:     run,
				Runtime: binding,
			}, nil
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
	runtime := RuntimeBinding{
		SchemaVersion:   runtimeSchemaVersion,
		ProjectID:       in.ProjectID,
		TrainID:         in.TrainID,
		WorktreePath:    worktreePath,
		AgentID:         in.ResolvedAgentID,
		SessionKey:      in.SessionKey,
		RestartRequired: false,
		StartedAt:       now,
	}
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
	return StartResult{
		Record:  record,
		Run:     dispatchedRun,
		Runtime: runtime,
	}, nil
}
