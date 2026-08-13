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

type RunRetirementInput struct {
	ProjectID           string
	ExpectedHubRevision string
	Apply               bool
}

type RunRetirementResult struct {
	DryRun       bool                        `json:"dry_run"`
	Applied      bool                        `json:"applied"`
	AlreadyDone  bool                        `json:"already_done"`
	ProjectID    string                      `json:"project_id"`
	HubBefore    string                      `json:"hub_before"`
	HubAfter     string                      `json:"hub_after"`
	ReceiptPath  string                      `json:"receipt_path"`
	ChangedPaths []string                    `json:"changed_paths"`
	Records      []model.RunRetirementRecord `json:"records"`
}

func runRetirementPath(projectID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/run-retirement/receipt.json"
}

func runRetirementEvidencePath(projectID, digest string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/run-retirement/records/" + digest + ".json"
}

// RetireRunRecords is a one-time, digest-guarded migration for every project.
// Records are copied into immutable evidence before operational /runs paths
// are removed. No runtime lookup can resolve this evidence by RunID.
func (s *Service) RetireRunRecords(ctx context.Context, in RunRetirementInput) (RunRetirementResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return RunRetirementResult{}, err
	}
	if in.Apply && in.ExpectedHubRevision == "" {
		return RunRetirementResult{}, fmt.Errorf("Run retirement apply requires expected_hub_revision")
	}
	revision, err := s.hubRevision(ctx)
	if err != nil {
		return RunRetirementResult{}, err
	}
	result := RunRetirementResult{
		DryRun:       !in.Apply,
		ProjectID:    in.ProjectID,
		HubBefore:    revision,
		ReceiptPath:  runRetirementPath(in.ProjectID),
		ChangedPaths: []string{},
		Records:      []model.RunRetirementRecord{},
	}
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
		run, _, decodeErr := decodeMigrationRun(raw)
		if decodeErr != nil {
			return result, fmt.Errorf("cannot losslessly retire %s: %w", sourcePath, decodeErr)
		}
		attemptStatus, statusErr := migrationAttemptStatus(run.Status, run.FinishedAt)
		if statusErr != nil {
			return result, fmt.Errorf("ACTIVE_LEGACY_RUN_REQUIRES_TRAIN_ATTEMPT_MIGRATION: %s: %w", sourcePath, statusErr)
		}
		if attemptStatus == model.TrainV2AttemptRunning {
			return result, fmt.Errorf("ACTIVE_LEGACY_RUN_REQUIRES_TRAIN_ATTEMPT_MIGRATION: %s: status %q must be losslessly attached to a canonical Train item Attempt before retirement", sourcePath, run.Status)
		}
		digest := digestBytes(raw)
		result.Records = append(result.Records, model.RunRetirementRecord{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: in.ProjectID, SourcePath: sourcePath, SourceSHA256: digest, OriginalRunID: run.ID, OriginalRunTaskID: run.TaskID, OriginalRunStatus: run.Status, OriginalRunJSONB64: encodeBytes(raw)})
	}
	if raw, readErr := s.Hub.ReadFile(ctx, result.ReceiptPath); readErr == nil {
		var receipt model.RunRetirementReceipt
		if err := decodeStrict(raw, &receipt); err != nil || receipt.ProjectID != in.ProjectID || (receipt.State != "pending" && receipt.State != "completed") {
			return result, fmt.Errorf("invalid Run retirement receipt")
		}
		for _, record := range receipt.Records {
			if err := model.ValidateRunRetirementRecord(record); err != nil {
				return result, fmt.Errorf("invalid Run retirement receipt: %w", err)
			}
		}
		if len(runPaths) != 0 {
			return result, fmt.Errorf("completed Run retirement still has operational records")
		}
		if receipt.State == "pending" {
			if !in.Apply {
				return result, fmt.Errorf("Run retirement receipt is pending; apply is required to complete it")
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
		if err := model.ValidateRunRetirementReceipt(receipt); err != nil {
			return result, err
		}
		result.AlreadyDone = true
		result.Applied = true
		result.HubAfter = revision
		result.Records = append([]model.RunRetirementRecord{}, receipt.Records...)
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
	receipt := model.RunRetirementReceipt{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: in.ProjectID, State: "pending", HubBefore: revision, HubAfter: revision, Records: append([]model.RunRetirementRecord{}, result.Records...), Reason: "remove pre-cutover Run storage after immutable digest-guarded migration evidence", CreatedAt: now, UpdatedAt: now}
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
			evidencePath := runRetirementEvidencePath(in.ProjectID, record.SourceSHA256)
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
		return result, fmt.Errorf("Run retirement committed with incomplete receipt: %w", err)
	}
	result.Applied = true
	result.HubAfter = receipt.HubAfter
	for _, record := range result.Records {
		result.ChangedPaths = append(result.ChangedPaths, runRetirementEvidencePath(in.ProjectID, record.SourceSHA256), record.SourcePath)
	}
	result.ChangedPaths = append(result.ChangedPaths, result.ReceiptPath)
	return result, nil
}

func encodeBytes(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func decodeBytes(value string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(value))
}
