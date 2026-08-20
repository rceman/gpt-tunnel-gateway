package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	runtimeLog "github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) integrationOperation(ctx context.Context, in TrainV2IntegrateInput, source, branch, target string, targetAncestorOfSource bool, now time.Time) (trainv2.IntegrationOperation, error) {
	digest, operationID, err := integrationRequestDigest(in, source, branch, target)
	if err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	current, readErr := s.readIntegrationOperation(ctx, in.ProjectID, in.TrainID)
	if readErr == nil {
		resumableTarget := target == source && current.Phase == trainv2.IntegrationPhasePostPending
		sameSourceAndBranch := current.SourceHead == source && current.TargetBranch == branch
		if current.Phase == trainv2.IntegrationPhasePrePending && strings.HasPrefix(current.RecoveryReason, migratedIntegrationRecoveryReasonPrefix) && sameSourceAndBranch && current.TargetBefore == target {
			if current.SupersedesOperationID == "" {
				return trainv2.IntegrationOperation{}, fmt.Errorf("migrated recovery operation identity is missing superseded operation")
			}
			expectedRecoveryDigest, expectedRecoveryID, digestErr := integrationRecoveryRequestDigest(in, source, branch, target, current.SupersedesOperationID)
			if digestErr != nil {
				return trainv2.IntegrationOperation{}, digestErr
			}
			if current.RequestSHA256 != expectedRecoveryDigest || current.OperationID != expectedRecoveryID {
				return trainv2.IntegrationOperation{}, fmt.Errorf("migrated recovery operation identity does not match its superseded operation")
			}
			return current, nil
		}
		if current.Phase == trainv2.IntegrationPhaseRecoveryRequired {
			if current.RequestSHA256 != digest || !sameSourceAndBranch || current.TargetBefore != target {
				return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: migrated identity does not match current source or target")
			}
			if err := s.verifyMigratedIntegrationRecovery(ctx, in.ProjectID, current); err != nil {
				return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: migrated evidence validation failed: %w", err)
			}
			recoveryDigest, recoveryOperationID, err := integrationRecoveryRequestDigest(in, source, branch, target, current.OperationID)
			if err != nil {
				return trainv2.IntegrationOperation{}, err
			}
			replacement := trainv2.IntegrationOperation{
				SchemaVersion:         1,
				OperationID:           recoveryOperationID,
				ProjectID:             in.ProjectID,
				TrainID:               in.TrainID,
				RequestSHA256:         recoveryDigest,
				SourceHead:            source,
				TargetBranch:          branch,
				TargetBefore:          target,
				Phase:                 trainv2.IntegrationPhasePrePending,
				SupersedesOperationID: current.OperationID,
				RecoveryReason:        migratedIntegrationRecoveryReasonPrefix + ": " + current.OperationID,
				UpdatedAt:             now,
			}
			if err := s.replaceRecoveredIntegrationOperation(ctx, current, replacement); err != nil {
				return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: %w", err)
			}
			return replacement, nil
		}
		recoverablePostPendingPrefix := current.Phase == trainv2.IntegrationPhasePostPending &&
			current.TargetBranch == branch &&
			target == current.SourceHead &&
			targetAncestorOfSource &&
			current.SourceHead != source
		identityMismatch := current.RequestSHA256 != digest || !sameSourceAndBranch || (current.TargetBefore != target && !resumableTarget)
		if identityMismatch {
			if resumableTarget && sameSourceAndBranch {
				return current, nil
			}
			if recoverablePostPendingPrefix {
				replacement := trainv2.IntegrationOperation{
					SchemaVersion:         1,
					OperationID:           operationID,
					ProjectID:             in.ProjectID,
					TrainID:               in.TrainID,
					RequestSHA256:         digest,
					SourceHead:            source,
					TargetBranch:          branch,
					TargetBefore:          target,
					Phase:                 trainv2.IntegrationPhasePrePending,
					SupersedesOperationID: current.OperationID,
					RecoveryReason:        "superseded post_pending prefix operation after proving current source descends from target",
					UpdatedAt:             now,
				}
				if err := s.replaceStaleIntegrationOperation(ctx, current, replacement); err != nil {
					return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: %w", err)
				}
				return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: post_pending prefix archived; retry with current source identity")
			}
			if current.Phase != trainv2.IntegrationPhasePrePending || current.PreResult != "" {
				return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: durable identity does not match current evidence")
			}
			replacement := trainv2.IntegrationOperation{
				SchemaVersion:         1,
				OperationID:           operationID,
				ProjectID:             in.ProjectID,
				TrainID:               in.TrainID,
				RequestSHA256:         digest,
				SourceHead:            source,
				TargetBranch:          branch,
				TargetBefore:          target,
				Phase:                 trainv2.IntegrationPhasePrePending,
				SupersedesOperationID: current.OperationID,
				RecoveryReason:        "replaced untouched pre_pending operation with current Train source and target identity",
				UpdatedAt:             now,
			}
			if err := s.replaceStaleIntegrationOperation(ctx, current, replacement); err != nil {
				return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: %w", err)
			}
			return trainv2.IntegrationOperation{}, fmt.Errorf("integration operation recovery_required: stale pre_pending operation archived; retry with current identity")
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
