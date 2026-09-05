package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// activeTrainAttempt is the generic Train/Attempt projection used by active
// Agent and Train paths. It is preserved independently of the removed
// supervision subsystem.
type activeTrainAttempt struct {
	Train   model.TrainV2
	Start   model.TrainV2StartRecord
	Runtime trainv2.RuntimeBinding
	Item    model.TrainV2Item
	Attempt model.TrainV2Attempt
}

func (s *Service) trainV2ActiveAttempt(ctx context.Context, projectID string) (activeTrainAttempt, bool, error) {
	if s.Durability != nil {
		return s.trainV2ActiveAttemptLocal(ctx, projectID)
	}
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{ProjectID: projectID, Limit: model.MaxTrainV2Items})
	if err != nil {
		return activeTrainAttempt{}, false, err
	}
	for _, train := range trains.Trains {
		if train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked {
			continue
		}
		var start model.TrainV2StartRecord
		path := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + train.ID + ".json"
		if err := s.Hub.ReadJSON(ctx, path, &start); err != nil {
			continue
		}
		runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
		if err != nil || start.CurrentItemPosition < 0 || start.CurrentItemPosition >= len(train.Items) || start.CurrentAttemptNumber == 0 {
			continue
		}
		item := train.Items[start.CurrentItemPosition]
		if item.TaskID != start.CurrentTaskID || start.CurrentAttemptNumber > uint64(len(item.Attempts)) {
			continue
		}
		attempt := item.Attempts[start.CurrentAttemptNumber-1]
		if attempt.Status != model.TrainV2AttemptRunning {
			continue
		}
		if err := validateActiveTrainAttempt(train, start, runtime); err != nil {
			return activeTrainAttempt{}, false, err
		}
		return activeTrainAttempt{Train: train, Start: start, Runtime: runtime, Item: item, Attempt: attempt}, true, nil
	}
	return activeTrainAttempt{}, false, nil
}

func (s *Service) trainV2ActiveAttemptLocal(ctx context.Context, projectID string) (activeTrainAttempt, bool, error) {
	trains, err := s.sharedTrains(ctx, projectID)
	if err != nil {
		return activeTrainAttempt{}, false, err
	}
	local, err := s.projectConfig(projectID)
	if err != nil {
		return activeTrainAttempt{}, false, err
	}
	integrationBranch := local.DefaultBranch
	if configuration, configurationErr := s.ProjectConfigurationRead(ctx, projectID); configurationErr == nil && configuration.Workflow.IntegrationBranch != "" {
		integrationBranch = configuration.Workflow.IntegrationBranch
	}
	for _, train := range trains {
		if train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked {
			continue
		}
		runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
		if runtimeErr != nil || runtime.RestartRequired || runtime.ItemPosition < 0 || runtime.ItemPosition >= len(train.Items) {
			continue
		}
		item := train.Items[runtime.ItemPosition]
		if item.TaskID != runtime.TaskID || item.Status != model.TrainV2ItemRunning || item.ActiveAttemptNumber != runtime.AttemptNumber || runtime.AttemptNumber == 0 || runtime.AttemptNumber > uint64(len(item.Attempts)) {
			continue
		}
		attempt := item.Attempts[runtime.AttemptNumber-1]
		if attempt.Status != model.TrainV2AttemptRunning || attempt.AgentID != runtime.AgentID || attempt.AirelaySessionKey != runtime.SessionKey {
			continue
		}
		start := model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: projectID, TrainID: train.ID, Status: model.TrainV2StartActive, IntegrationBranch: integrationBranch, BaseRevision: attempt.StartHead, LaneBranch: "train/" + train.ID, CurrentItemPosition: item.Position, CurrentAttemptNumber: attempt.Number, CurrentTaskID: item.TaskID, CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256, StartedAt: attempt.StartedAt}
		if err := model.ValidateTrainV2StartRecord(start); err != nil {
			return activeTrainAttempt{}, false, err
		}
		if err := validateActiveTrainAttempt(train, start, runtime); err != nil {
			return activeTrainAttempt{}, false, err
		}
		return activeTrainAttempt{Train: train, Start: start, Runtime: runtime, Item: item, Attempt: attempt}, true, nil
	}
	return activeTrainAttempt{}, false, nil
}

func validateActiveTrainAttempt(train model.TrainV2, start model.TrainV2StartRecord, runtime trainv2.RuntimeBinding) error {
	if err := model.ValidateTrainV2(train); err != nil {
		return err
	}
	if err := model.ValidateTrainV2StartRecord(start); err != nil {
		return err
	}
	if err := trainv2.ValidateRuntimeBindingShape(runtime); err != nil {
		return err
	}
	if runtime.RestartRequired {
		return fmt.Errorf("train cannot bind a retired execution generation")
	}
	if start.ProjectID != train.ProjectID || start.TrainID != train.ID || runtime.ProjectID != train.ProjectID || runtime.TrainID != train.ID || runtime.ItemPosition != start.CurrentItemPosition || runtime.AttemptNumber != start.CurrentAttemptNumber || runtime.TaskID != start.CurrentTaskID || runtime.AgentID == "" || runtime.SessionKey == "" {
		return fmt.Errorf("train identity mismatch")
	}
	if start.CurrentItemPosition < 0 || start.CurrentItemPosition >= len(train.Items) {
		return fmt.Errorf("train item position is invalid")
	}
	item := train.Items[start.CurrentItemPosition]
	if item.TaskID != start.CurrentTaskID || item.Status != model.TrainV2ItemRunning || start.CurrentAttemptNumber == 0 || start.CurrentAttemptNumber > uint64(len(item.Attempts)) {
		return fmt.Errorf("train current item is not running under the exact Attempt")
	}
	attempt := item.Attempts[start.CurrentAttemptNumber-1]
	if attempt.AgentID != runtime.AgentID || attempt.AirelaySessionKey != runtime.SessionKey || attempt.StartHead != start.BaseRevision {
		return fmt.Errorf("train Attempt snapshot mismatch")
	}
	return nil
}
