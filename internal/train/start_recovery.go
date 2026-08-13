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
	if err != nil || record.Status != model.TrainV2StartActive || record.CurrentAttemptNumber == 0 {
		return StartResult{}, fmt.Errorf("durable Train Attempt record is unavailable")
	}
	if record.CurrentItemPosition < 0 || record.CurrentItemPosition >= len(deps.Train.Items) {
		return StartResult{}, fmt.Errorf("durable Train Attempt item is unavailable")
	}
	item := deps.Train.Items[record.CurrentItemPosition]
	attempt, err := itemAttempt(item, record.CurrentAttemptNumber)
	if err != nil {
		return StartResult{}, err
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
		Record:       record,
		ItemPosition: record.CurrentItemPosition,
		Attempt:      attempt,
		Runtime:      binding,
	}, nil
}
