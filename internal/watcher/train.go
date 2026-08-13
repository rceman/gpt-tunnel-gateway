package watcher

import (
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type TrainBinding struct {
	ProjectID     string `json:"project_id"`
	TrainID       string `json:"train_id"`
	ItemPosition  int    `json:"item_position"`
	TaskID        string `json:"task_id"`
	AttemptNumber uint64 `json:"attempt_number"`
	AgentID       string `json:"agent_id"`
	SessionKey    string `json:"session_key"`
	WorktreePath  string `json:"worktree_path"`
	LaneBranch    string `json:"lane_branch"`
}

// BindTrainAttempt attaches optional supervision to an exact Train item and
// item-local Attempt. No Run record participates in this binding.
func BindTrainAttempt(train model.TrainV2, start model.TrainV2StartRecord, runtime trainv2.RuntimeBinding) (TrainBinding, error) {
	if err := model.ValidateTrainV2(train); err != nil {
		return TrainBinding{}, err
	}
	if err := model.ValidateTrainV2StartRecord(start); err != nil {
		return TrainBinding{}, err
	}
	if err := trainv2.ValidateRuntimeBindingShape(runtime); err != nil {
		return TrainBinding{}, err
	}
	if runtime.RestartRequired {
		return TrainBinding{}, fmt.Errorf("train watcher cannot bind a retired execution generation")
	}
	if start.ProjectID != train.ProjectID || start.TrainID != train.ID || runtime.ProjectID != train.ProjectID || runtime.TrainID != train.ID || runtime.ItemPosition != start.CurrentItemPosition || runtime.AttemptNumber != start.CurrentAttemptNumber || runtime.TaskID != start.CurrentTaskID || runtime.AgentID == "" || runtime.SessionKey == "" {
		return TrainBinding{}, fmt.Errorf("train watcher identity mismatch")
	}
	if start.CurrentItemPosition < 0 || start.CurrentItemPosition >= len(train.Items) {
		return TrainBinding{}, fmt.Errorf("train watcher item position is invalid")
	}
	item := train.Items[start.CurrentItemPosition]
	if item.TaskID != start.CurrentTaskID || item.Status != model.TrainV2ItemRunning || start.CurrentAttemptNumber == 0 || start.CurrentAttemptNumber > uint64(len(item.Attempts)) {
		return TrainBinding{}, fmt.Errorf("train watcher current item is not running under the exact Attempt")
	}
	attempt := item.Attempts[start.CurrentAttemptNumber-1]
	if attempt.AgentID != runtime.AgentID || attempt.AirelaySessionKey != runtime.SessionKey || attempt.StartHead != start.BaseRevision {
		return TrainBinding{}, fmt.Errorf("train watcher Attempt snapshot mismatch")
	}
	return TrainBinding{ProjectID: train.ProjectID, TrainID: train.ID, ItemPosition: item.Position, TaskID: item.TaskID, AttemptNumber: attempt.Number, AgentID: runtime.AgentID, SessionKey: runtime.SessionKey, WorktreePath: runtime.WorktreePath, LaneBranch: start.LaneBranch}, nil
}

func CurrentItem(train model.TrainV2, taskID string) (model.TrainV2Item, bool) {
	for _, item := range train.Items {
		if item.TaskID == taskID {
			return item, true
		}
	}
	return model.TrainV2Item{}, false
}

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
	GatewayID    string
	SessionKey   string
	WorktreePath string
	LaneBranch   string
}

func PlanAutoAdvance(train model.TrainV2, binding TrainBinding, attemptStatus string) (AdvancePlan, bool, error) {
	if attemptStatus != model.TrainV2AttemptSucceeded {
		return AdvancePlan{}, false, nil
	}
	if train.Status == model.TrainV2Paused || train.Status == model.TrainV2Blocked {
		return AdvancePlan{}, false, fmt.Errorf("train is not advanceable: %s", train.Status)
	}
	current, ok := CurrentItem(train, binding.TaskID)
	if !ok || current.Status != model.TrainV2ItemFinalized || current.Position != binding.ItemPosition || binding.AttemptNumber == 0 || binding.AttemptNumber > uint64(len(current.Attempts)) || current.Attempts[binding.AttemptNumber-1].Status != model.TrainV2AttemptSucceeded {
		return AdvancePlan{}, false, fmt.Errorf("current train item is not finalized by the bound Attempt")
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
	return AdvancePlan{Current: current, Next: next, AgentID: binding.AgentID, GatewayID: train.ProjectID, SessionKey: binding.SessionKey, WorktreePath: binding.WorktreePath, LaneBranch: binding.LaneBranch}, true, nil
}

func StartNextItem(train model.TrainV2, plan AdvancePlan, startHead string, now time.Time) (model.TrainV2, error) {
	if now.IsZero() || model.ValidateCommitSHA(startHead) != nil || model.ValidateObjectIdentifier(plan.AgentID) != nil || plan.SessionKey == "" {
		return model.TrainV2{}, fmt.Errorf("invalid next train item Attempt start")
	}
	if plan.Next.Position != plan.Current.Position+1 || train.Items[plan.Next.Position].Status != model.TrainV2ItemQueued {
		return model.TrainV2{}, fmt.Errorf("next train item is not the immediate queued successor")
	}
	updated := train
	item := &updated.Items[plan.Next.Position]
	item.Status = model.TrainV2ItemRunning
	item.Attempts = []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: plan.AgentID, GatewayID: plan.GatewayID, AirelaySessionKey: plan.SessionKey, StartHead: startHead, StartedAt: now}}
	item.ActiveAttemptNumber = 1
	updated.Status = model.TrainV2Running
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}
