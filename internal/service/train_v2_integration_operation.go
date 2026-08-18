package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	runtimeLog "github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const migratedIntegrationRecoveryReasonPrefix = "resumed migrated recovery_required integration"

func trainV2IntegrationOperationPath(projectID, trainID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/" + trainID + ".integration-operation.json"
}

func trainV2IntegrationOperationHistoryPath(projectID, trainID, operationID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/integration-operation-history/" + trainID + "/" + operationID + ".json"
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

func (s *Service) replaceStaleIntegrationOperation(ctx context.Context, previous, replacement trainv2.IntegrationOperation) error {
	if err := trainv2.ValidateIntegrationOperation(previous); err != nil {
		return err
	}
	if err := trainv2.ValidateIntegrationOperation(replacement); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: recover stale Train integration operation "+previous.OperationID, func(worktree string) ([]string, error) {
		currentPath := trainV2IntegrationOperationPath(previous.ProjectID, previous.TrainID)
		var current trainv2.IntegrationOperation
		if err := readWorktreeJSON(worktree, currentPath, &current); err != nil {
			return nil, err
		}
		if current.OperationID != previous.OperationID || current.RequestSHA256 != previous.RequestSHA256 || current.SourceHead != previous.SourceHead || current.TargetBefore != previous.TargetBefore || current.Phase != previous.Phase {
			return nil, fmt.Errorf("integration operation changed before stale recovery")
		}
		currentBytes, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(currentPath)))
		if err != nil {
			return nil, err
		}
		historyPath := trainV2IntegrationOperationHistoryPath(previous.ProjectID, previous.TrainID, previous.OperationID)
		historyAbs := filepath.Join(worktree, filepath.FromSlash(historyPath))
		if historyBytes, readErr := os.ReadFile(historyAbs); readErr == nil {
			if !bytes.Equal(historyBytes, currentBytes) {
				return nil, fmt.Errorf("stale integration operation history identity mismatch")
			}
		} else if !os.IsNotExist(readErr) {
			return nil, readErr
		} else if err := hub.WriteText(worktree, historyPath, string(currentBytes)); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, currentPath, replacement); err != nil {
			return nil, err
		}
		return []string{historyPath, currentPath}, nil
	})
	return err
}

func integrationRecoveryRequestDigest(in TrainV2IntegrateInput, source, branch, target, previousOperationID string) (string, string, error) {
	payload, err := json.Marshal(struct {
		ProjectID, TrainID, Source, Branch, Target, PreviousOperationID string
	}{in.ProjectID, in.TrainID, source, branch, target, previousOperationID})
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(payload)
	hexDigest := hex.EncodeToString(digest[:])
	return hexDigest, "GTW-INTEGRATE-" + hexDigest[:24], nil
}

func (s *Service) verifyMigratedIntegrationRecovery(ctx context.Context, projectID string, current trainv2.IntegrationOperation) error {
	rawReceipt, err := s.Hub.ReadFile(ctx, trainV2LegacyMigrationReceiptPath(projectID))
	if err != nil {
		return fmt.Errorf("read legacy migration receipt: %w", err)
	}
	var receipt model.TrainV2LegacyStateMigrationReceipt
	if err := decodeStrict(rawReceipt, &receipt); err != nil {
		return fmt.Errorf("decode legacy migration receipt: %w", err)
	}
	if err := model.ValidateTrainV2LegacyStateMigrationReceipt(receipt); err != nil {
		return fmt.Errorf("validate legacy migration receipt: %w", err)
	}
	if receipt.ProjectID != projectID || receipt.State != "completed" {
		return fmt.Errorf("legacy migration receipt is not completed for project %s", projectID)
	}
	var record *model.TrainV2LegacyStateMigrationRecord
	for i := range receipt.Records {
		candidate := &receipt.Records[i]
		if candidate.TrainID == current.TrainID && candidate.Action == TrainV2LegacyActionRecoverIntegrate {
			record = candidate
			break
		}
	}
	if record == nil {
		return fmt.Errorf("no exact recover_integration migration record for %s", current.TrainID)
	}
	if record.TrainPath != s.trainV2Path(projectID, current.TrainID) || record.IntegrationPath != trainV2IntegrationOperationPath(projectID, current.TrainID) {
		return fmt.Errorf("migrated integration evidence path mismatch for %s", current.TrainID)
	}
	trainRaw, err := s.Hub.ReadFile(ctx, record.TrainPath)
	originalTrainRaw, decodeErr := decodeBytes(record.OriginalTrainJSONB64)
	if err != nil || digestBytes(trainRaw) != record.TrainSHA256 || decodeErr != nil || !bytes.Equal(trainRaw, originalTrainRaw) {
		return fmt.Errorf("migrated Train digest mismatch for %s", current.TrainID)
	}
	var train model.TrainV2
	if err := decodeStrict(trainRaw, &train); err != nil || train.ID != current.TrainID || train.ProjectID != projectID {
		return fmt.Errorf("migrated Train identity mismatch for %s", current.TrainID)
	}
	if err := model.ValidateTrainV2(train); err != nil {
		return fmt.Errorf("migrated Train validation failed for %s: %w", current.TrainID, err)
	}
	evidenceRaw, err := s.Hub.ReadFile(ctx, trainV2LegacyMigrationEvidencePath(projectID, current.TrainID, record.TrainSHA256))
	if err != nil {
		return fmt.Errorf("read migrated integration evidence: %w", err)
	}
	var evidence model.TrainV2LegacyStateMigrationRecord
	if err := decodeStrict(evidenceRaw, &evidence); err != nil || evidence != *record {
		return fmt.Errorf("migrated integration evidence mismatch for %s", current.TrainID)
	}
	historyPath := trainV2IntegrationOperationHistoryPath(projectID, current.TrainID, current.OperationID)
	historyRaw, err := s.Hub.ReadFile(ctx, historyPath)
	if err != nil {
		return fmt.Errorf("read archived integration operation: %w", err)
	}
	originalOperationRaw, err := decodeBytes(record.OriginalIntegrationJSONB64)
	if err != nil || !bytes.Equal(historyRaw, originalOperationRaw) || digestBytes(historyRaw) != record.IntegrationSHA256 {
		return fmt.Errorf("archived integration operation digest mismatch for %s", current.TrainID)
	}
	var archived trainv2.IntegrationOperation
	if err := decodeStrict(historyRaw, &archived); err != nil || archived.Phase != trainv2.IntegrationPhasePrePending {
		return fmt.Errorf("archived integration operation is not the original pre_pending record")
	}
	if archived.OperationID != current.OperationID || archived.ProjectID != current.ProjectID || archived.TrainID != current.TrainID || archived.RequestSHA256 != current.RequestSHA256 || archived.SourceHead != current.SourceHead || archived.TargetBranch != current.TargetBranch || archived.TargetBefore != current.TargetBefore {
		return fmt.Errorf("archived integration operation identity mismatch for %s", current.TrainID)
	}
	mutationRaw, err := os.ReadFile(filepath.Join(s.Config.StateDir, filepath.FromSlash(record.MutationPath)))
	if err != nil || digestBytes(mutationRaw) != record.MutationSHA256 {
		return fmt.Errorf("failed integration mutation digest mismatch for %s", current.TrainID)
	}
	originalMutationRaw, err := decodeBytes(record.OriginalMutationJSONB64)
	if err != nil || !bytes.Equal(mutationRaw, originalMutationRaw) {
		return fmt.Errorf("failed integration mutation evidence changed for %s", current.TrainID)
	}
	var mutation durableMutationOperation
	if err := decodeStrict(mutationRaw, &mutation); err != nil || mutation.OperationID != filepath.Base(strings.TrimSuffix(record.MutationPath, ".json")) || mutation.Kind != "train-v2-integrate" || mutation.ProjectID != projectID || mutation.Status != "failed" {
		return fmt.Errorf("failed integration mutation identity mismatch for %s", current.TrainID)
	}
	var input TrainV2IntegrateInput
	if err := decodeStrict(mutation.Input, &input); err != nil || input.ProjectID != projectID || input.TrainID != current.TrainID {
		return fmt.Errorf("failed integration mutation target mismatch for %s", current.TrainID)
	}
	return nil
}

func (s *Service) replaceRecoveredIntegrationOperation(ctx context.Context, previous, replacement trainv2.IntegrationOperation) error {
	if err := trainv2.ValidateIntegrationOperation(previous); err != nil {
		return err
	}
	if err := trainv2.ValidateIntegrationOperation(replacement); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: resume migrated Train integration", func(worktree string) ([]string, error) {
		path := trainV2IntegrationOperationPath(previous.ProjectID, previous.TrainID)
		var current trainv2.IntegrationOperation
		if err := readWorktreeJSON(worktree, path, &current); err != nil {
			return nil, err
		}
		if current.OperationID != previous.OperationID || current.RequestSHA256 != previous.RequestSHA256 || current.SourceHead != previous.SourceHead || current.TargetBranch != previous.TargetBranch || current.TargetBefore != previous.TargetBefore || current.Phase != trainv2.IntegrationPhaseRecoveryRequired {
			return nil, fmt.Errorf("migrated integration operation changed before recovery")
		}
		if err := hub.WriteJSON(worktree, path, replacement); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

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
