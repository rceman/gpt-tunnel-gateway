package train

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
	return StartResult{
		Record:  record,
		Run:     run,
		Runtime: binding,
	}, nil
}
