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
		return s.sharedTaskAttempt(ctx, projectID, taskID)
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

func (s *Service) sharedTaskAttempt(ctx context.Context, projectID, taskID string) (currentTaskAttempt, error) {
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
		runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
		if runtimeErr != nil || runtime.TaskID != taskID || runtime.ItemPosition < 0 || runtime.ItemPosition >= len(train.Items) {
			continue
		}
		item := train.Items[runtime.ItemPosition]
		if item.TaskID != taskID || runtime.AttemptNumber == 0 || runtime.AttemptNumber > uint64(len(item.Attempts)) {
			return currentTaskAttempt{}, fmt.Errorf("Task current TrainItem binding is invalid")
		}
		attempt := item.Attempts[runtime.AttemptNumber-1]
		if attempt.Status != model.TrainV2AttemptRunning {
			return currentTaskAttempt{}, fmt.Errorf("Task does not have a running Attempt")
		}
		if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
			return currentTaskAttempt{}, err
		}
		found = currentTaskAttempt{Train: train, Item: item, Attempt: attempt, Runtime: runtime}
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
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{
		ProjectID: projectID,
		Limit:     model.MaxTrainV2Items,
	})
	if err != nil {
		return "", err
	}
	trainID := ""
	for _, train := range trains.Trains {
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
