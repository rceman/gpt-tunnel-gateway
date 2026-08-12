package train

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// NextRunInput contains only the durable identity needed to continue a Train
// lane. Host-local session/worktree values are copied from the current Run by
// the service adapter and are never accepted as caller-controlled paths.
type NextRunInput struct {
	Current      model.Run
	Next         model.TrainV2Item
	RunID        string
	BaseRevision string
	StateDir     string
	CreatedAt    time.Time
}

// BuildNextRun creates the created-state Run for the immediate queued Train
// item. Dispatch is a separate transport side effect owned by the service.
func BuildNextRun(in NextRunInput) (model.Run, error) {
	if err := model.ValidateRun(in.Current); err != nil || in.Current.TrainID == "" || in.Current.Historical {
		return model.Run{}, fmt.Errorf("current Train Run is not advanceable")
	}
	if in.Next.Status != model.TrainV2ItemQueued || model.ValidateCanonicalTaskID(in.Next.TaskID) != nil || len(in.Next.TaskRevisionSHA256) != 64 {
		return model.Run{}, fmt.Errorf("next Train item is not a valid queued task")
	}
	taskID, _, err := model.ParseRunID(in.RunID)
	if err != nil || taskID != in.Next.TaskID {
		return model.Run{}, fmt.Errorf("next Run ID does not bind the queued task")
	}
	if err := model.ValidateCommitSHA(in.BaseRevision); err != nil || in.StateDir == "" || in.CreatedAt.IsZero() {
		return model.Run{}, fmt.Errorf("next Train Run identity is incomplete")
	}
	run := in.Current
	run.ID = in.RunID
	run.TaskID = in.Next.TaskID
	run.TaskSHA256 = in.Next.TaskRevisionSHA256
	run.TaskRevision, run.TaskRevisionSHA256, run.TaskRunNumber = 0, "", 0
	run.BaseRevision = in.BaseRevision
	run.Status = "created"
	run.DispatchMessage, run.DispatchStdout, run.DispatchStderr = "", "", ""
	run.DispatchExitCode = nil
	run.CompletionPath = filepath.Join(in.StateDir, "runs", in.RunID, "completion.json")
	run.CreatedAt = in.CreatedAt
	run.DispatchedAt, run.LastRepromptAt, run.FinishedAt = nil, nil, nil
	run.RepromptCount = 0
	if err := model.ValidateRun(run); err != nil {
		return model.Run{}, err
	}
	return run, nil
}
