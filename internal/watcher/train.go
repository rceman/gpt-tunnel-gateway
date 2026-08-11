package watcher

import (
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainBinding is the watcher-owned projection of one Train lane. The
// session and worktree are host-local values recovered from Gateway runtime
// state; the Train/Run records remain the portable identity authority.
type TrainBinding struct {
	ProjectID    string `json:"project_id"`
	TrainID      string `json:"train_id"`
	ItemPosition int    `json:"item_position"`
	TaskID       string `json:"task_id"`
	RunID        string `json:"run_id"`
	AgentID      string `json:"agent_id"`
	SessionKey   string `json:"session_key"`
	WorktreePath string `json:"worktree_path"`
	LaneBranch   string `json:"lane_branch"`
}

// BindTrainRun verifies that watcher supervision is attached to the one
// running Train item and its sole Agent/session. It does not consult Plan or
// ExecutionGroups.
func BindTrainRun(train model.TrainV2, start model.TrainV2StartRecord, runtime trainv2.RuntimeBinding, run model.Run) (TrainBinding, error) {
	if err := model.ValidateTrainV2(train); err != nil {
		return TrainBinding{}, err
	}
	if err := model.ValidateTrainV2StartRecord(start); err != nil {
		return TrainBinding{}, err
	}
	if err := trainv2.ValidateRuntimeBindingShape(runtime); err != nil {
		return TrainBinding{}, err
	}
	if err := model.ValidateRun(run); err != nil {
		return TrainBinding{}, err
	}
	if start.ProjectID != train.ProjectID || start.TrainID != train.ID || runtime.ProjectID != train.ProjectID || runtime.TrainID != train.ID || run.ProjectID != train.ProjectID || run.TrainID != train.ID || run.ID != start.RunID || run.TaskID != start.CurrentTaskID || run.AgentID != runtime.AgentID || run.SessionKey != runtime.SessionKey || run.Branch != start.LaneBranch || run.BaseRevision != start.BaseRevision {
		return TrainBinding{}, fmt.Errorf("train watcher identity mismatch")
	}
	item, ok := CurrentItem(train, start.CurrentTaskID)
	if !ok || item.Status != model.TrainV2ItemRunning || item.RunID != run.ID || item.AgentID != run.AgentID || item.StartHead != start.BaseRevision {
		return TrainBinding{}, fmt.Errorf("train watcher current item is not running under the exact Run")
	}
	return TrainBinding{ProjectID: train.ProjectID, TrainID: train.ID, ItemPosition: item.Position, TaskID: item.TaskID, RunID: run.ID, AgentID: runtime.AgentID, SessionKey: runtime.SessionKey, WorktreePath: runtime.WorktreePath, LaneBranch: start.LaneBranch}, nil
}

func CurrentItem(train model.TrainV2, taskID string) (model.TrainV2Item, bool) {
	for _, item := range train.Items {
		if item.TaskID == taskID {
			return item, true
		}
	}
	return model.TrainV2Item{}, false
}

// ReviewableItem returns immutable item proof without requiring the current
// lane head to remain equal to the reviewed checkpoint.
func ReviewableItem(train model.TrainV2, taskID string) (model.TrainV2Item, error) {
	item, ok := CurrentItem(train, taskID)
	if !ok || item.Proof == nil || (item.Status != model.TrainV2ItemFinalized && item.Status != model.TrainV2ItemReviewed && item.Status != model.TrainV2ItemBlocked) {
		return model.TrainV2Item{}, fmt.Errorf("train item %q has no immutable review proof", taskID)
	}
	return item, nil
}

type AdvancePlan struct {
	Current      model.TrainV2Item
	Next         model.TrainV2Item
	AgentID      string
	SessionKey   string
	WorktreePath string
	LaneBranch   string
}

// PlanAutoAdvance is fail-closed. A successful finalized item may advance
// only to the immediate queued successor, retaining the same runtime owner.
func PlanAutoAdvance(train model.TrainV2, binding TrainBinding, runStatus string) (AdvancePlan, bool, error) {
	if runStatus != "succeeded" {
		return AdvancePlan{}, false, nil
	}
	if train.Status == model.TrainV2Paused || train.Status == model.TrainV2Blocked {
		return AdvancePlan{}, false, fmt.Errorf("train is not advanceable: %s", train.Status)
	}
	current, ok := CurrentItem(train, binding.TaskID)
	if !ok || current.Status != model.TrainV2ItemFinalized || current.RunID != binding.RunID || current.AgentID != binding.AgentID {
		return AdvancePlan{}, false, fmt.Errorf("current train item is not finalized by the bound Run")
	}
	if current.Position+1 >= len(train.Items) {
		return AdvancePlan{}, false, nil
	}
	if train.Status == model.TrainV2ReadyForIntegration {
		return AdvancePlan{}, false, fmt.Errorf("Train is ready for integration and has an unresolved successor")
	}
	next := train.Items[current.Position+1]
	if next.Status != model.TrainV2ItemQueued {
		return AdvancePlan{}, false, fmt.Errorf("next train item is not queued")
	}
	return AdvancePlan{Current: current, Next: next, AgentID: binding.AgentID, SessionKey: binding.SessionKey, WorktreePath: binding.WorktreePath, LaneBranch: binding.LaneBranch}, true, nil
}

func StartNextItem(train model.TrainV2, plan AdvancePlan, runID, startHead string, now time.Time) (model.TrainV2, error) {
	if runID == "" || now.IsZero() {
		return model.TrainV2{}, fmt.Errorf("invalid next train item start")
	}
	if _, ok := CurrentItem(train, plan.Current.TaskID); !ok {
		return model.TrainV2{}, fmt.Errorf("current train item disappeared")
	}
	if plan.Next.Position != plan.Current.Position+1 || train.Items[plan.Next.Position].Status != model.TrainV2ItemQueued {
		return model.TrainV2{}, fmt.Errorf("next train item is not the immediate queued successor")
	}
	if model.ValidateCanonicalRunID(runID) != nil || model.ValidateCommitSHA(startHead) != nil || model.ValidateObjectIdentifier(plan.AgentID) != nil {
		return model.TrainV2{}, fmt.Errorf("invalid next train item identity")
	}
	updated := train
	item := &updated.Items[plan.Next.Position]
	item.Status = model.TrainV2ItemRunning
	item.RunID = runID
	item.AgentID = plan.AgentID
	item.StartHead = startHead
	updated.Status = model.TrainV2Running
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}
