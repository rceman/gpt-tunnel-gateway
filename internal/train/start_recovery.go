package train

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func recoverMissingRuntime(ctx context.Context, deps StartDependencies) (StartResult, error) {
	var record *model.TrainV2StartRecord
	var item model.TrainV2Item
	var attempt model.TrainV2Attempt
	for _, candidate := range deps.Train.Items {
		if candidate.ActiveAttemptNumber == 0 || candidate.ActiveAttemptNumber > uint64(len(candidate.Attempts)) {
			continue
		}
		candidateAttempt := candidate.Attempts[candidate.ActiveAttemptNumber-1]
		if candidateAttempt.Status != model.TrainV2AttemptRunning {
			continue
		}
		if record != nil {
			return StartResult{}, fmt.Errorf("durable Train Attempt authority is ambiguous")
		}
		item = candidate
		attempt = candidateAttempt
		derived := DeriveStartRecord(deps.Train, candidate, candidateAttempt, deps.Policy, deps.Project, candidateAttempt.StartedAt)
		record = &derived
	}
	if record == nil {
		return StartResult{}, fmt.Errorf("durable Train Attempt record is unavailable")
	}
	path, compactErr := CompactWorktreePath(deps.StateDir, deps.ProjectCode, deps.Train.ID)
	compact := compactErr == nil
	if !compact {
		path = ExpectedWorktreePath(deps.StateDir, deps.Project.ID, deps.Train.ID)
	}
	if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
		return StartResult{}, fmt.Errorf("server-owned Train worktree is unavailable")
	}
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     deps.Project.ID,
		ProjectCode:   map[bool]string{true: deps.ProjectCode, false: ""}[compact],
		TrainID:       deps.Train.ID,
		WorktreePath:  path,
		AgentID:       attempt.AgentID,
		SessionKey:    attempt.AirelaySessionKey,
		ItemPosition:  record.CurrentItemPosition,
		TaskID:        item.TaskID,
		AttemptNumber: attempt.Number,
		StartedAt:     record.StartedAt,
	}
	if err := ValidateRuntimeBinding(binding, deps.StateDir); err != nil {
		return StartResult{}, err
	}
	if err := fsutil.WriteJSONAtomic(runtimePath(deps.StateDir, deps.Project.ID, deps.Train.ID), binding, 0o600); err != nil {
		return StartResult{}, err
	}
	return StartResult{
		Record:       *record,
		ItemPosition: record.CurrentItemPosition,
		Attempt:      attempt,
		Runtime:      binding,
	}, nil
}
