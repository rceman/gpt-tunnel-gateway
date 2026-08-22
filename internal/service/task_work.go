package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

var errTaskHasNoCurrentAttempt = errors.New("Task is not the current active TrainItem")

type currentTaskAttempt struct {
	Train   model.TrainV2
	Item    model.TrainV2Item
	Attempt model.TrainV2Attempt
	Runtime trainv2.RuntimeBinding
}

// taskAttempt resolves the one active TrainItem owned by taskID. The caller
// supplies only the project and Task identity; Train identity is discovered
// from the current Train state and never accepted as caller authority.
func (s *Service) taskAttempt(ctx context.Context, projectID, taskID string) (currentTaskAttempt, error) {
	if err := requireTrainV2Authoring(ctx, s, projectID); err != nil {
		return currentTaskAttempt{}, err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return currentTaskAttempt{}, err
	}
	if _, err := s.TaskAuthoringRead(ctx, projectID, taskID); err != nil {
		return currentTaskAttempt{}, err
	}
	if s.Durability != nil {
		return s.taskAttemptShared(ctx, projectID, taskID)
	}
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{
		ProjectID: projectID,
		Limit:     model.MaxTrainV2Items,
	})
	if err != nil {
		return currentTaskAttempt{}, err
	}
	var found currentTaskAttempt
	foundCount := 0
	for _, train := range trains.Trains {
		if train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked {
			continue
		}
		startPath := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + train.ID + ".json"
		var start model.TrainV2StartRecord
		if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
			continue
		}
		if start.CurrentTaskID != taskID || start.CurrentItemPosition < 0 || start.CurrentItemPosition >= len(train.Items) || start.CurrentAttemptNumber == 0 {
			continue
		}
		item := train.Items[start.CurrentItemPosition]
		if item.TaskID != taskID || item.ActiveAttemptNumber != start.CurrentAttemptNumber || start.CurrentAttemptNumber > uint64(len(item.Attempts)) {
			return currentTaskAttempt{}, fmt.Errorf("Task current TrainItem binding is invalid")
		}
		attempt := item.Attempts[start.CurrentAttemptNumber-1]
		if attempt.Status != model.TrainV2AttemptRunning {
			return currentTaskAttempt{}, fmt.Errorf("Task does not have a running Attempt")
		}
		runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
		if err != nil {
			return currentTaskAttempt{}, fmt.Errorf("Task Attempt runtime is unavailable: %w", err)
		}
		if runtime.ItemPosition != item.Position || runtime.TaskID != taskID || runtime.AttemptNumber != attempt.Number {
			return currentTaskAttempt{}, fmt.Errorf("Task Attempt runtime ownership mismatch")
		}
		if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
			return currentTaskAttempt{}, err
		}
		found = currentTaskAttempt{
			Train:   train,
			Item:    item,
			Attempt: attempt,
			Runtime: runtime,
		}
		foundCount++
	}
	if foundCount == 0 {
		return currentTaskAttempt{}, errTaskHasNoCurrentAttempt
	}
	if foundCount != 1 {
		return currentTaskAttempt{}, fmt.Errorf("Task has ambiguous current TrainItem ownership")
	}
	return found, nil
}

func (s *Service) taskAttemptShared(ctx context.Context, projectID, taskID string) (currentTaskAttempt, error) {
	trains, err := s.sharedTrains(ctx, projectID)
	if err != nil {
		return currentTaskAttempt{}, err
	}
	var found currentTaskAttempt
	foundCount := 0
	for _, train := range trains {
		if train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked {
			continue
		}
		for _, item := range train.Items {
			if item.TaskID != taskID || item.ActiveAttemptNumber == 0 {
				continue
			}
			attempt, err := trainv2Attempt(item, item.ActiveAttemptNumber)
			if err != nil {
				return currentTaskAttempt{}, err
			}
			if attempt.Status != model.TrainV2AttemptRunning {
				return currentTaskAttempt{}, fmt.Errorf("Task does not have a running Attempt")
			}
			runtime, runtimeErr := s.sharedRuntimeForAttempt(projectID, train, item, attempt)
			if runtimeErr != nil {
				return currentTaskAttempt{}, fmt.Errorf("Task Attempt runtime is unavailable: %w", runtimeErr)
			}
			if runtime.TrainID != train.ID || runtime.ItemPosition != item.Position || runtime.TaskID != taskID || runtime.AttemptNumber != attempt.Number {
				return currentTaskAttempt{}, fmt.Errorf("Task current TrainItem binding is invalid")
			}
			if runtime.AgentID != attempt.AgentID || runtime.SessionKey != attempt.AirelaySessionKey {
				return currentTaskAttempt{}, fmt.Errorf("Task Attempt runtime execution snapshot mismatch")
			}
			if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
				return currentTaskAttempt{}, err
			}
			found = currentTaskAttempt{Train: train, Item: item, Attempt: attempt, Runtime: runtime}
			foundCount++
		}
	}
	if foundCount == 0 {
		return currentTaskAttempt{}, errTaskHasNoCurrentAttempt
	}
	if foundCount != 1 {
		return currentTaskAttempt{}, fmt.Errorf("Task has ambiguous current TrainItem ownership")
	}
	return found, nil
}

func trainv2Attempt(item model.TrainV2Item, number uint64) (model.TrainV2Attempt, error) {
	if number == 0 || number > uint64(len(item.Attempts)) || item.Attempts[number-1].Number != number {
		return model.TrainV2Attempt{}, fmt.Errorf("Train item has no exact Attempt %d", number)
	}
	return item.Attempts[number-1], nil
}

func (s *Service) TaskWork(ctx context.Context, in TaskWorkInput) (TaskWorkResult, error) {
	if in.ProjectID == "" {
		task, err := s.TaskAuthoringFind(ctx, in.TaskID)
		if err != nil {
			return TaskWorkResult{}, err
		}
		in.ProjectID = task.ProjectID
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskWorkResult{}, err
	}
	current, err := s.taskAttempt(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		if !errors.Is(err, errTaskHasNoCurrentAttempt) {
			return TaskWorkResult{}, err
		}
		trainID, resolveErr := s.resolvePlannedTaskTrain(ctx, in.ProjectID, in.TaskID)
		if resolveErr != nil {
			return TaskWorkResult{}, resolveErr
		}
		startedBy := in.StartedBy
		if startedBy == "" {
			startedBy = "task-work"
		}
		if _, startErr := s.TrainV2Start(ctx, TrainV2StartInput{
			ProjectID:            in.ProjectID,
			TrainID:              trainID,
			StartedBy:            startedBy,
			AgentID:              in.AgentID,
			RecommendedReasoning: in.RecommendedReasoning,
			WriteOptions:         in.WriteOptions,
		}); startErr != nil {
			return TaskWorkResult{}, startErr
		}
		current, err = s.taskAttempt(ctx, in.ProjectID, in.TaskID)
		if err != nil {
			return TaskWorkResult{}, err
		}
	}
	packet, err := s.materializeTrainV2Packet(ctx, current.Train, current.Item, current.Attempt, current.Runtime)
	if err != nil {
		return TaskWorkResult{}, err
	}
	text, err := fsutil.ReadFileBounded(packet.Path, maxTrainV2PacketBytes)
	if err != nil {
		return TaskWorkResult{}, err
	}
	return TaskWorkResult{
		TaskID:        in.TaskID,
		TrainID:       current.Train.ID,
		ItemPosition:  current.Item.Position,
		AttemptNumber: current.Attempt.Number,
		AttemptStatus: current.Attempt.Status,
		PacketPath:    packet.Path,
		WorktreePath:  filepath.Clean(packet.WorktreePath),
		Text:          string(text),
	}, nil
}

func (s *Service) TaskFinalize(ctx context.Context, in TaskFinalizeInput) (TrainV2AttemptFinalizeResult, error) {
	return s.finalizeTaskByIdentity(ctx, in)
}

func (s *Service) resolvePlannedTaskTrain(ctx context.Context, projectID, taskID string) (string, error) {
	var trains []model.TrainV2
	var err error
	if s.Durability != nil {
		trains, err = s.trainV2ListShared(ctx, projectID, model.MaxTrainV2Items)
	} else {
		var listed TrainV2ListResult
		listed, err = s.TrainV2List(ctx, TrainV2ListInput{ProjectID: projectID, Limit: model.MaxTrainV2Items})
		trains = listed.Trains
	}
	if err != nil {
		return "", err
	}
	trainID := ""
	for _, train := range trains {
		for _, item := range train.Items {
			if item.TaskID != taskID {
				continue
			}
			if train.Status != model.TrainV2Planned || item.Status != model.TrainV2ItemQueued || len(item.Attempts) != 0 || item.ActiveAttemptNumber != 0 {
				return "", fmt.Errorf("Task is admitted to a non-startable TrainItem")
			}
			if trainID != "" {
				return "", fmt.Errorf("Task has ambiguous planned Train ownership")
			}
			trainID = train.ID
		}
	}
	if trainID == "" {
		return "", errTaskHasNoCurrentAttempt
	}
	return trainID, nil
}
