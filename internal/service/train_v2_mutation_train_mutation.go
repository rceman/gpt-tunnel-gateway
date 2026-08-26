package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) TrainV2Create(ctx context.Context, in TrainV2CreateInput) (model.TrainV2, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if in.CreatedBy == "" || strings.ContainsAny(in.CreatedBy, "\x00\r\n") {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	tasks, err := s.trainV2AdmissionTasksShared(ctx, in.ProjectID, in.TaskIDs)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	trains, err := s.sharedTrains(ctx, in.ProjectID)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	trainID, err := nextSharedTrainID(project.ProjectCode, trains)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	created, err := trainv2.New(in.ProjectID, trainID, in.CreatedBy, tasks, s.durableNow())
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		operationID = "train-v2-create-" + trainID
	}
	if err := s.commitSharedTrain(ctx, operationID, created, "train-v2-create"); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	return created, OperationResult{OperationID: operationID, ProjectID: in.ProjectID, Status: created.Status}, nil
}

func (s *Service) TrainV2Add(ctx context.Context, in TrainV2AddInput) (model.TrainV2, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if in.AddedBy == "" || strings.ContainsAny(in.AddedBy, "\x00\r\n") || in.ExpectedRevision < 1 {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("expected_revision and added_by are required")
	}
	current, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if current.Revision != in.ExpectedRevision {
		return model.TrainV2{}, OperationResult{}, trainRevisionStatusConflict("precondition", "revision", in.ExpectedRevision, current.Revision, current.Status)
	}
	if !trainV2AddableStatus(current.Status) {
		return model.TrainV2{}, OperationResult{}, trainRevisionStatusConflict("precondition", "status", in.ExpectedRevision, current.Revision, current.Status)
	}
	if current.Status == model.TrainV2ReadyForIntegration && s.Durability != nil {
		if receipt, readErr := s.Durability.ReadSharedEntity(ctx, "integration_receipt", sqlitestore.SharedIntegrationReceiptID(in.ProjectID, in.TrainID)); readErr == nil {
			var integration trainv2.IntegrationReceipt
			if err := json.Unmarshal(receipt.Payload, &integration); err != nil {
				return model.TrainV2{}, OperationResult{}, err
			}
			if integration.Status == "completed" {
				return model.TrainV2{}, OperationResult{}, trainIntegrationReceiptConflict("receipt", current.Revision, current.Status, integration.Status)
			}
		} else if !IsNotFound(readErr) {
			return model.TrainV2{}, OperationResult{}, readErr
		}
	}
	if len(current.Items)+len(in.TaskIDs) > model.MaxTrainV2Items {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("train v2 item limit exceeded")
	}
	tasks, err := s.trainV2AdmissionTasksShared(ctx, in.ProjectID, in.TaskIDs)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	updated, err := trainv2.Append(current, tasks, s.durableNow())
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		operationID = "train-v2-add-" + in.TrainID + fmt.Sprintf("-%d", in.ExpectedRevision)
	}
	if err := s.commitSharedTrain(ctx, operationID, updated, "train-v2-add"); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	return updated, OperationResult{OperationID: operationID, ProjectID: in.ProjectID, Status: updated.Status}, nil
}

func nextSharedTrainID(projectCode string, trains []model.TrainV2) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	var next uint64 = 1
	for _, train := range trains {
		code, number, err := model.ParseTrainV2ID(train.ID)
		if err == nil && code == projectCode && number >= next {
			next = number + 1
		}
	}
	return model.FormatTrainV2ID(projectCode, next)
}
