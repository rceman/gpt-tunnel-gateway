package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	runtimeLog "github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func trainV2IntegrationOperationPath(projectID, trainID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/" + trainID + ".integration-operation.json"
}

func integrationRequestDigest(in TrainV2IntegrateInput, source, branch, target string) (string, string, error) {
	payload, err := json.Marshal(struct {
		ProjectID, TrainID, Source, Branch, Target string
	}{in.ProjectID, in.TrainID, source, branch, target})
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(payload)
	hexDigest := hex.EncodeToString(digest[:])
	return hexDigest, "GTW-INTEGRATE-" + hexDigest[:24], nil
}

func (s *Service) readIntegrationOperation(ctx context.Context, projectID, trainID string) (trainv2.IntegrationOperation, error) {
	var operation trainv2.IntegrationOperation
	if err := s.Hub.ReadJSON(ctx, trainV2IntegrationOperationPath(projectID, trainID), &operation); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	return operation, nil
}

func (s *Service) persistIntegrationOperation(ctx context.Context, operation trainv2.IntegrationOperation) error {
	if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: persist Train integration operation "+operation.OperationID, func(worktree string) ([]string, error) {
		path := trainV2IntegrationOperationPath(operation.ProjectID, operation.TrainID)
		if err := hub.WriteJSON(worktree, path, operation); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) integrationOperation(ctx context.Context, in TrainV2IntegrateInput, source, branch, target string, now time.Time) (trainv2.IntegrationOperation, error) {
	digest, operationID, err := integrationRequestDigest(in, source, branch, target)
	if err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	current, readErr := s.readIntegrationOperation(ctx, in.ProjectID, in.TrainID)
	if readErr == nil {
		resumableTarget := target == source && current.Phase != trainv2.IntegrationPhasePrePending && current.Phase != trainv2.IntegrationPhasePreComplete
		if current.RequestSHA256 != digest || current.SourceHead != source || current.TargetBranch != branch || (current.TargetBefore != target && !resumableTarget) {
			return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: durable identity does not match current evidence")
		}
		return current, nil
	}
	if !IsNotFound(readErr) {
		return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: %w", readErr)
	}
	current = trainv2.IntegrationOperation{SchemaVersion: 1, OperationID: operationID, ProjectID: in.ProjectID, TrainID: in.TrainID, RequestSHA256: digest, SourceHead: source, TargetBranch: branch, TargetBefore: target, Phase: trainv2.IntegrationPhasePrePending, UpdatedAt: now}
	if err := s.persistIntegrationOperation(ctx, current); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	return current, nil
}

func (s *Service) advanceIntegrationOperation(ctx context.Context, operation trainv2.IntegrationOperation, phase, result string) (trainv2.IntegrationOperation, error) {
	operation.Phase = phase
	if phase == trainv2.IntegrationPhasePreComplete {
		operation.PreResult = result
	}
	if phase == trainv2.IntegrationPhaseCompleted || phase == trainv2.IntegrationPhasePostPending || phase == trainv2.IntegrationPhaseIntegrateComplete {
		operation.PostResult = result
	}
	operation.UpdatedAt = time.Now().UTC()
	if err := s.persistIntegrationOperation(ctx, operation); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	if s.Config.StateDir != "" {
		_ = runtimeLog.New(s.Config.StateDir).Append(runtimeLog.Event{Timestamp: operation.UpdatedAt, Level: "info", Component: "train", Event: "integration_phase", OperationID: operation.OperationID, ProjectID: operation.ProjectID, Message: phase})
	}
	return operation, nil
}
