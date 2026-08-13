package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2RunRetirementInput struct {
	ProjectID           string
	ExpectedHubRevision string
	Apply               bool
}

type TrainV2RunRetirementResult struct {
	DryRun       bool                               `json:"dry_run"`
	Applied      bool                               `json:"applied"`
	AlreadyDone  bool                               `json:"already_done"`
	ProjectID    string                             `json:"project_id"`
	HubBefore    string                             `json:"hub_before"`
	HubAfter     string                             `json:"hub_after"`
	ReceiptPath  string                             `json:"receipt_path"`
	ChangedPaths []string                           `json:"changed_paths"`
	Records      []model.TrainV2RunRetirementRecord `json:"records"`
}

func trainV2RunRetirementPath(projectID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-run-retirement/receipt.json"
}

func trainV2RunRetirementEvidencePath(projectID, digest string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-run-retirement/records/" + digest + ".json"
}

// TrainV2RetireRuns is a one-time, digest-guarded migration. The records are
// copied into immutable migration evidence before their operational /runs paths
// are removed. No runtime lookup can resolve this evidence by RunID.
func (s *Service) TrainV2RetireRuns(ctx context.Context, in TrainV2RunRetirementInput) (TrainV2RunRetirementResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2RunRetirementResult{}, err
	}
	if in.Apply && in.ExpectedHubRevision == "" {
		return TrainV2RunRetirementResult{}, fmt.Errorf("Train-v2 Run retirement apply requires expected_hub_revision")
	}
	configuration, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return TrainV2RunRetirementResult{}, err
	}
	if configuration.ExecutionModel != "train_v2" {
		return TrainV2RunRetirementResult{}, fmt.Errorf("Run retirement is only valid for train_v2 projects")
	}
	revision, err := s.hubRevision(ctx)
	if err != nil {
		return TrainV2RunRetirementResult{}, err
	}
	result := TrainV2RunRetirementResult{DryRun: !in.Apply, ProjectID: in.ProjectID, HubBefore: revision, ReceiptPath: trainV2RunRetirementPath(in.ProjectID), ChangedPaths: []string{}, Records: []model.TrainV2RunRetirementRecord{}}
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != revision {
		return result, fmt.Errorf("HUB_REVISION_CONFLICT: expected %s, got %s", in.ExpectedHubRevision, revision)
	}
	runPaths, err := s.Hub.List(ctx, s.projectPrefix(in.ProjectID)+"/runs", "/run.json")
	if err != nil {
		return result, err
	}
	for _, sourcePath := range runPaths {
		raw, readErr := s.Hub.ReadFile(ctx, sourcePath)
		if readErr != nil {
			return result, readErr
		}
		run, _, decodeErr := model.DecodeRunRecord(raw)
		if decodeErr != nil {
			return result, fmt.Errorf("cannot losslessly retire %s: %w", sourcePath, decodeErr)
		}
		digest := digestBytes(raw)
		result.Records = append(result.Records, model.TrainV2RunRetirementRecord{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: in.ProjectID, SourcePath: sourcePath, SourceSHA256: digest, OriginalRunID: run.ID, OriginalRunTaskID: run.TaskID, OriginalRunStatus: run.Status, OriginalRunJSONB64: encodeBytes(raw)})
	}
	if raw, readErr := s.Hub.ReadFile(ctx, result.ReceiptPath); readErr == nil {
		var receipt model.TrainV2RunRetirementReceipt
		if err := decodeStrict(raw, &receipt); err != nil || receipt.ProjectID != in.ProjectID || (receipt.State != "pending" && receipt.State != "completed") {
			return result, fmt.Errorf("invalid Train-v2 Run retirement receipt")
		}
		for _, record := range receipt.Records {
			if err := model.ValidateTrainV2RunRetirementRecord(record); err != nil {
				return result, fmt.Errorf("invalid Train-v2 Run retirement receipt: %w", err)
			}
		}
		if len(runPaths) != 0 {
			return result, fmt.Errorf("completed Train-v2 Run retirement still has operational records")
		}
		if receipt.State == "pending" {
			if !in.Apply {
				return result, fmt.Errorf("Train-v2 Run retirement receipt is pending; apply is required to complete it")
			}
			receipt.State = "completed"
			receipt.HubAfter = revision
			receipt.UpdatedAt = nowUTC()
			tx, txErr := s.Hub.Transact(ctx, revision, "gateway: complete Train-v2 Run retirement "+in.ProjectID, func(worktree string) ([]string, error) {
				if err := hub.WriteJSON(worktree, result.ReceiptPath, receipt); err != nil {
					return nil, err
				}
				return []string{result.ReceiptPath}, nil
			})
			if txErr != nil {
				return result, txErr
			}
			receipt.HubAfter = tx.After
		}
		if err := model.ValidateTrainV2RunRetirementReceipt(receipt); err != nil {
			return result, err
		}
		result.AlreadyDone = true
		result.Applied = true
		result.HubAfter = revision
		result.Records = append([]model.TrainV2RunRetirementRecord{}, receipt.Records...)
		return result, nil
	} else if !IsNotFound(readErr) {
		return result, readErr
	}
	if !in.Apply {
		return result, nil
	}
	if _, err := s.Hub.Backup(ctx, "train-v2-run-retirement"); err != nil {
		return result, err
	}
	now := nowUTC()
	receipt := model.TrainV2RunRetirementReceipt{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: in.ProjectID, State: "pending", HubBefore: revision, HubAfter: revision, Records: append([]model.TrainV2RunRetirementRecord{}, result.Records...), Reason: "remove pre-cutover Train-v2 Run storage after immutable digest-guarded migration evidence", CreatedAt: now, UpdatedAt: now}
	tx, err := s.Hub.Transact(ctx, revision, "gateway: retire Train-v2 Run storage "+in.ProjectID, func(worktree string) ([]string, error) {
		paths := make([]string, 0, len(result.Records)*2+1)
		for _, record := range result.Records {
			raw, err := decodeBytes(record.OriginalRunJSONB64)
			if err != nil || digestBytes(raw) != record.SourceSHA256 {
				return nil, fmt.Errorf("retirement evidence digest mismatch for %s", record.SourcePath)
			}
			currentPath := filepath.Join(worktree, filepath.FromSlash(record.SourcePath))
			current, err := os.ReadFile(currentPath)
			if err != nil || digestBytes(current) != record.SourceSHA256 || string(current) != string(raw) {
				return nil, fmt.Errorf("Run source changed before retirement: %s", record.SourcePath)
			}
			evidencePath := trainV2RunRetirementEvidencePath(in.ProjectID, record.SourceSHA256)
			if err := hub.WriteJSON(worktree, evidencePath, record); err != nil {
				return nil, err
			}
			paths = append(paths, evidencePath)
			if err := os.Remove(currentPath); err != nil {
				return nil, err
			}
			paths = append(paths, record.SourcePath)
		}
		if err := hub.WriteJSON(worktree, result.ReceiptPath, receipt); err != nil {
			return nil, err
		}
		return append(paths, result.ReceiptPath), nil
	})
	if err != nil {
		return result, err
	}
	receipt.State = "completed"
	receipt.HubAfter = tx.After
	receipt.UpdatedAt = nowUTC()
	if _, err := s.Hub.Transact(ctx, tx.After, "gateway: complete Train-v2 Run retirement "+in.ProjectID, func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, result.ReceiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{result.ReceiptPath}, nil
	}); err != nil {
		return result, fmt.Errorf("Train-v2 Run retirement committed with incomplete receipt: %w", err)
	}
	result.Applied = true
	result.HubAfter = receipt.HubAfter
	for _, record := range result.Records {
		result.ChangedPaths = append(result.ChangedPaths, trainV2RunRetirementEvidencePath(in.ProjectID, record.SourceSHA256), record.SourcePath)
	}
	result.ChangedPaths = append(result.ChangedPaths, result.ReceiptPath)
	return result, nil
}

func encodeBytes(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func decodeBytes(value string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(value))
}
