package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func trainV2CutoverPath(projectID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/configuration/train-v2-cutover.json"
}

// TrainV2Cutover is the only service operation that changes the project's
// execution authority. All migration and runtime proofs are collected before
// the Hub transaction; the transaction writes configuration and receipt
// together so a partial cutover cannot leave two writable authorities.
func (s *Service) TrainV2Cutover(ctx context.Context, in TrainV2CutoverInput) (model.TrainV2CutoverReceipt, OperationResult, error) {
	if err := RequireWorkflowPolicyAuthority(ctx); err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.UpdatedBy) == "" || strings.ContainsAny(in.UpdatedBy, "\x00\r\n") {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("updated_by is required")
	}
	configuration, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	if configuration.ExecutionModel == "train_v2" {
		var receipt model.TrainV2CutoverReceipt
		if err := s.Hub.ReadJSON(ctx, trainV2CutoverPath(in.ProjectID), &receipt); err != nil {
			return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("train_v2 configuration has no valid cutover receipt: %w", err)
		}
		if err := model.ValidateTrainV2CutoverReceipt(receipt); err != nil {
			return model.TrainV2CutoverReceipt{}, OperationResult{}, err
		}
		return receipt, OperationResult{
			ProjectID: in.ProjectID,
			Status:    "already_activated",
		}, nil
	}
	if configuration.ExecutionModel != "legacy" && configuration.ExecutionModel != "" {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("unsupported project execution authority")
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, in.ProjectID)
	if err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	local, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	worktree, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil || !worktree.Clean {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("train_v2 cutover requires a clean project worktree")
	}
	if err := s.Git.Refresh(ctx, local); err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("refresh integration mirror: %w", err)
	}
	sourceHead, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil || !clean || branch != policy.IntegrationBranch || model.ValidateCommitSHA(sourceHead) != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("project source is not a clean integration checkout")
	}
	mirrorHead, exists, err := s.Git.MirrorBranchHead(ctx, local, policy.IntegrationBranch)
	if err != nil || !exists || mirrorHead != sourceHead {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("integration checkout is not synchronized with the managed mirror")
	}
	if s.runtimeSourceProver == nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("exact runtime source proof is not configured")
	}
	sourceProof, err := s.runtimeSourceProver(ctx, local, sourceHead)
	if err != nil || sourceProof.SourceHead != sourceHead {
		if err != nil {
			return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("exact runtime source proof failed: %w", err)
		}
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("exact runtime source proof did not match the integration head")
	}
	runtime, err := (controller.Controller{Config: s.Config, ConfigPath: s.ConfigPath}).Status(ctx)
	if err != nil || !runtime.GatewayReady || !runtime.TunnelReady || !runtime.VersionMatch {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("exact target runtime is not active and version-matched")
	}
	runs, err := s.RunList(ctx, in.ProjectID)
	if err != nil && !IsNotFound(err) {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("historical run compatibility check failed: %w", err)
	}
	runHistoryOK := err == nil || IsNotFound(err)
	activeLegacy := 0
	for _, run := range runs {
		if run.TrainID == "" && operationalActiveRun(run) {
			activeLegacy++
		}
	}
	legacyTrains, legacyTrainErr := s.TaskTrainList(ctx, in.ProjectID)
	if legacyTrainErr != nil && !IsNotFound(legacyTrainErr) {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("legacy task-train compatibility check failed: %w", legacyTrainErr)
	}
	for _, legacyTrain := range legacyTrains {
		if legacyTrain.Status != model.TaskTrainCompleted {
			return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("active legacy TaskTrain-v1 %q blocks Train v2 cutover", model.CanonicalTaskTrainID(legacyTrain))
		}
	}
	trains, err := s.readTrainV2Records(ctx, in.ProjectID)
	if err != nil && !IsNotFound(err) {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	activeTrains := 0
	for _, train := range trains {
		if train.Status == model.TrainV2Running || train.Status == model.TrainV2Paused || train.Status == model.TrainV2Blocked || train.Status == model.TrainV2ReadyForIntegration {
			activeTrains++
		}
		if receipt, readErr := s.readTrainV2IntegrationReceipt(ctx, in.ProjectID, train.ID); readErr == nil && receipt.Status != "completed" {
			activeTrains++
		} else if readErr != nil && !IsNotFound(readErr) {
			return model.TrainV2CutoverReceipt{}, OperationResult{}, fmt.Errorf("invalid Train integration history: %w", readErr)
		}
	}
	evidence := trainv2.CutoverEvidence{
		CurrentExecutionModel: configuration.ExecutionModel, MaterializationAcknowledged: in.MaterializationAcknowledged,
		PlanRetirementAcknowledged: in.PlanRetirementAcknowledged, ActiveLegacyRuns: activeLegacy, ActiveTrains: activeTrains,
		HistoricalCompatibilityOK: runHistoryOK, IntegrationClean: true, SourceHead: sourceHead, MirrorHead: mirrorHead,
		RuntimeReady: runtime.GatewayReady && runtime.TunnelReady, RuntimeVersionMatch: runtime.VersionMatch,
		RegisteredActions: append([]string{}, trainv2.RequiredCutoverActions...),
	}
	if err := trainv2.ValidateCutover(evidence); err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	updated, err := trainv2.CutoverConfiguration(configuration, in.UpdatedBy, now)
	if err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	receipt, err := trainv2.NewCutoverReceipt(in.ProjectID, updated, sourceHead, sourceHead, in.MaterializationAcknowledged, in.PlanRetirementAcknowledged, in.UpdatedBy, now)
	if err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.TrainV2CutoverReceipt{}, OperationResult{}, err
		}
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: activate train_v2 authority "+in.ProjectID, func(worktree string) ([]string, error) {
		var latest model.ProjectConfiguration
		if err := readWorktreeJSON(worktree, s.projectConfigurationPath(in.ProjectID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != configuration.Revision || (latest.ExecutionModel != "legacy" && latest.ExecutionModel != "") {
			return nil, fmt.Errorf("project execution authority changed before cutover")
		}
		candidate, err := trainv2.CutoverConfiguration(latest, in.UpdatedBy, now)
		if err != nil {
			return nil, err
		}
		receipt.ConfigurationRevision = candidate.Revision
		if err := hub.WriteJSON(worktree, s.projectConfigurationPath(in.ProjectID), candidate); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, trainV2CutoverPath(in.ProjectID), receipt); err != nil {
			return nil, err
		}
		return []string{s.projectConfigurationPath(in.ProjectID), trainV2CutoverPath(in.ProjectID)}, nil
	})
	if err != nil {
		return model.TrainV2CutoverReceipt{}, OperationResult{}, err
	}
	return receipt, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "activated",
	}, nil
}

func (s *Service) readTrainV2Records(ctx context.Context, projectID string) ([]model.TrainV2, error) {
	paths, err := s.Hub.List(ctx, s.trainV2Root(projectID), ".json")
	if err != nil {
		return nil, err
	}
	trains := make([]model.TrainV2, 0, len(paths))
	for _, path := range paths {
		var train model.TrainV2
		if err := s.Hub.ReadJSON(ctx, path, &train); err != nil {
			return nil, err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return nil, err
		}
		trains = append(trains, train)
	}
	return trains, nil
}
