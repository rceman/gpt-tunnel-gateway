package train

import (
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// ValidateTaskIDs is the small, storage-independent admission boundary used
// by the Gateway service adapter and the Train package tests.
func ValidateTaskIDs(taskIDs []string) error {
	if len(taskIDs) < 1 || len(taskIDs) > model.MaxTrainV2Items {
		return fmt.Errorf("invalid train v2 task count")
	}
	seen := map[string]bool{}
	for _, taskID := range taskIDs {
		if model.ValidateCanonicalTaskID(taskID) != nil || seen[taskID] {
			return fmt.Errorf("invalid or duplicate train v2 task %q", taskID)
		}
		seen[taskID] = true
	}
	return nil
}

func ReadyItems(tasks []model.TaskAuthoring, now time.Time, offset int) ([]model.TrainV2Item, error) {
	if err := ValidateTaskIDs(taskIDs(tasks)); err != nil {
		return nil, err
	}
	items := make([]model.TrainV2Item, 0, len(tasks))
	for index, task := range tasks {
		if task.Status != model.TaskAuthoringReady || task.ReadySeal == nil || task.ReadySeal.Revision != task.Revision || task.ReadySeal.RevisionSHA256 != task.RevisionSHA256 || model.ValidateTaskAuthoring(task) != nil {
			return nil, fmt.Errorf("task %q is not an exact ready train_v2 Task", task.ID)
		}
		items = append(items, model.TrainV2Item{Position: offset + index, TaskID: task.ID, TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256, Status: model.TrainV2ItemQueued, AddedAt: now})
	}
	return items, nil
}

func New(projectID, id, createdBy string, tasks []model.TaskAuthoring, now time.Time) (model.TrainV2, error) {
	items, err := ReadyItems(tasks, now, 0)
	if err != nil {
		return model.TrainV2{}, err
	}
	train := model.TrainV2{SchemaVersion: model.TrainV2SchemaVersion, ID: id, ProjectID: projectID, Revision: 1, Items: items, Status: model.TrainV2Planned, CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now}
	if err := model.ValidateTrainV2(train); err != nil {
		return model.TrainV2{}, err
	}
	return train, nil
}

func Append(current model.TrainV2, tasks []model.TaskAuthoring, now time.Time) (model.TrainV2, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	if len(current.Items)+len(tasks) > model.MaxTrainV2Items {
		return model.TrainV2{}, fmt.Errorf("train v2 item limit exceeded")
	}
	items, err := ReadyItems(tasks, now, len(current.Items))
	if err != nil {
		return model.TrainV2{}, err
	}
	if current.Status == model.TrainV2ReadyForIntegration {
		current.Status = model.TrainV2Planned
		for _, item := range current.Items {
			if item.Status != model.TrainV2ItemQueued || len(item.Attempts) > 0 {
				current.Status = model.TrainV2Running
				break
			}
		}
	}
	current.FullProof = nil
	current.Items = append(current.Items, items...)
	current.Revision++
	current.UpdatedAt = now
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	return current, nil
}

// ValidateUnadmitted keeps the storage adapter from admitting a Task twice
// without making the Train package depend on Hub or Git.
func ValidateUnadmitted(existing []model.TrainV2, taskIDs []string) error {
	if err := ValidateTaskIDs(taskIDs); err != nil {
		return err
	}
	for _, train := range existing {
		if err := model.ValidateTrainV2(train); err != nil {
			return err
		}
		if train.Status == model.TrainV2Completed {
			continue
		}
		for _, item := range train.Items {
			for _, candidate := range taskIDs {
				if item.TaskID == candidate {
					return fmt.Errorf("task %q already belongs to train %q", candidate, train.ID)
				}
			}
		}
	}
	return nil
}

// TaskAdmittedToNonterminal reports ownership that still constrains authoring.
// Completed Train history is intentionally not a permanent edit lock.
func TaskAdmittedToNonterminal(existing []model.TrainV2, taskID string) bool {
	for _, train := range existing {
		if train.Status == model.TrainV2Completed {
			continue
		}
		for _, item := range train.Items {
			if item.TaskID == taskID {
				return true
			}
		}
	}
	return false
}

func taskIDs(tasks []model.TaskAuthoring) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}
