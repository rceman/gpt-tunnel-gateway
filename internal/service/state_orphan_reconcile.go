package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type OrphanRunReconcileInput struct {
	ProjectID              string
	RunID                  string
	ExpectedHubRevision    string
	ExpectedOriginalSHA256 string
	Actor                  string
	Session                string
	Reason                 string
	Apply                  bool
}

type OrphanRunReconcileResult struct {
	DryRun             bool              `json:"dry_run"`
	Applied            bool              `json:"applied"`
	AlreadyReconciled  bool              `json:"already_reconciled"`
	State              string            `json:"state"`
	ProjectID          string            `json:"project_id"`
	RunID              string            `json:"run_id"`
	TaskID             string            `json:"task_id"`
	SourcePath         string            `json:"source_path"`
	RecoveryPath       string            `json:"recovery_path"`
	ReceiptPath        string            `json:"receipt_path"`
	OriginalSHA256     string            `json:"original_sha256"`
	Backup             *hub.BackupResult `json:"backup,omitempty"`
	HubRevisionBefore  string            `json:"hub_revision_before,omitempty"`
	HubRevisionAfter   string            `json:"hub_revision_after,omitempty"`
	ReceiptHubRevision string            `json:"receipt_hub_revision,omitempty"`
	ChangedPaths       []string          `json:"changed_paths,omitempty"`
	Check              StateCheckResult  `json:"check"`
}

const defaultOrphanRunReason = "explicit recovery of an operational run that references no durable task"

func (s *Service) orphanRecoveryPath(projectID, runID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateObjectIdentifier(runID) != nil {
		return "../invalid-orphan-recovery"
	}
	return s.projectPrefix(projectID) + "/recovery/orphan-runs/" + runID + ".json"
}

func (s *Service) orphanRecoveryReceiptPath(projectID, runID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateObjectIdentifier(runID) != nil {
		return "../invalid-orphan-recovery-receipt"
	}
	return s.projectPrefix(projectID) + "/recovery/orphan-runs/" + runID + ".receipt.json"
}

func (s *Service) ReconcileOrphanRun(ctx context.Context, in OrphanRunReconcileInput) (OrphanRunReconcileResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return OrphanRunReconcileResult{}, err
	}
	if err := model.ValidateObjectIdentifier(in.RunID); err != nil {
		return OrphanRunReconcileResult{}, fmt.Errorf("run_id: %w", err)
	}
	if strings.TrimSpace(in.Actor) == "" {
		in.Actor = s.Config.GatewayID
	}
	if strings.TrimSpace(in.Reason) == "" {
		in.Reason = defaultOrphanRunReason
	}
	sourcePath := s.runPath(in.ProjectID, in.RunID)
	recoveryPath := s.orphanRecoveryPath(in.ProjectID, in.RunID)
	receiptPath := s.orphanRecoveryReceiptPath(in.ProjectID, in.RunID)
	result := OrphanRunReconcileResult{
		DryRun:       !in.Apply,
		State:        model.OrphanRunQuarantined,
		ProjectID:    in.ProjectID,
		RunID:        in.RunID,
		SourcePath:   sourcePath,
		RecoveryPath: recoveryPath,
		ReceiptPath:  receiptPath,
		ChangedPaths: []string{},
	}

	check, err := s.StateCheck(ctx)
	if err != nil {
		return result, err
	}
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != check.HubRevision {
		return result, fmt.Errorf("HUB_REVISION_CONFLICT expected=%s actual=%s", in.ExpectedHubRevision, check.HubRevision)
	}
	result.Check = check

	if recoveryRaw, recoveryErr := s.Hub.ReadFile(ctx, recoveryPath); recoveryErr == nil {
		var recovery model.OrphanRunRecovery
		if err := decodeStrict(recoveryRaw, &recovery); err != nil {
			return result, fmt.Errorf("decode orphan recovery record: %w", err)
		}
		if err := model.ValidateOrphanRunRecovery(recovery); err != nil {
			return result, err
		}
		if recovery.ProjectID != in.ProjectID || recovery.RunID != in.RunID {
			return result, fmt.Errorf("orphan recovery identity mismatch")
		}
		if _, runErr := s.Hub.ReadFile(ctx, sourcePath); runErr == nil {
			return result, fmt.Errorf("orphan recovery record exists while operational run remains")
		} else if !IsNotFound(runErr) {
			return result, runErr
		}
		receiptRaw, receiptErr := s.Hub.ReadFile(ctx, receiptPath)
		if receiptErr != nil {
			return result, fmt.Errorf("orphan recovery receipt is missing: %w", receiptErr)
		}
		var receipt model.OrphanRunRecoveryReceipt
		if err := decodeStrict(receiptRaw, &receipt); err != nil {
			return result, fmt.Errorf("decode orphan recovery receipt: %w", err)
		}
		if err := model.ValidateOrphanRunRecoveryReceipt(receipt); err != nil {
			return result, err
		}
		if receipt.ProjectID != in.ProjectID || receipt.RunID != in.RunID || receipt.OriginalSHA256 != recovery.OriginalSHA256 {
			return result, fmt.Errorf("orphan recovery receipt identity mismatch")
		}
		if receipt.ReceiptStatus == model.OrphanReceiptPending {
			completed, receiptTx, err := s.completeOrphanRecoveryReceipt(ctx, check.HubRevision, receiptPath, receipt)
			if err != nil {
				return result, fmt.Errorf("orphan recovery receipt remains pending: %w", err)
			}
			receipt = completed
			result.ReceiptHubRevision = receiptTx.After
		} else {
			result.ReceiptHubRevision = check.HubRevision
		}
		result.AlreadyReconciled = true
		result.Applied = true
		result.TaskID = ""
		result.OriginalSHA256 = recovery.OriginalSHA256
		result.HubRevisionBefore = receipt.HubRevisionBefore
		result.HubRevisionAfter = receipt.HubRevisionAfter
		if !check.Valid {
			return result, fmt.Errorf("reconciled orphan state remains invalid: %d issue(s)", len(check.Issues))
		}
		return result, nil
	} else if !IsNotFound(recoveryErr) {
		return result, recoveryErr
	}

	raw, err := s.Hub.ReadFile(ctx, sourcePath)
	if err != nil {
		return result, fmt.Errorf("read orphan run: %w", err)
	}
	run, historical, err := model.DecodeRunRecord(raw)
	if err != nil {
		return result, fmt.Errorf("decode orphan run: %w", err)
	}
	if historical || run.ID != in.RunID || run.ProjectID != in.ProjectID {
		return result, fmt.Errorf("target is not the expected current run")
	}
	if err := model.ValidateRun(run); err != nil {
		return result, fmt.Errorf("validate orphan run: %w", err)
	}
	result.TaskID = run.TaskID
	result.OriginalSHA256 = digestBytes(raw)
	if in.ExpectedOriginalSHA256 != "" && in.ExpectedOriginalSHA256 != result.OriginalSHA256 {
		return result, fmt.Errorf("orphan run content changed before reconciliation")
	}
	if taskRaw, taskErr := s.Hub.ReadFile(ctx, s.taskPath(in.ProjectID, run.TaskID)); taskErr == nil {
		_ = taskRaw
		return result, fmt.Errorf("refusing reconciliation: referenced task %s exists", run.TaskID)
	} else if !IsNotFound(taskErr) {
		return result, taskErr
	}
	if !hasExactIssue(check, "RUN_WITHOUT_TASK", in.ProjectID, in.RunID, run.TaskID) {
		return result, fmt.Errorf("target run is not the exact current RUN_WITHOUT_TASK state")
	}
	if in.ExpectedHubRevision == "" {
		in.ExpectedHubRevision = check.HubRevision
	}
	if !in.Apply {
		return result, nil
	}

	backup, err := s.Hub.Backup(ctx, "orphan-run-reconcile")
	if err != nil {
		return result, err
	}
	recovery := model.OrphanRunRecovery{
		SchemaVersion:      model.OrphanRunRecoverySchemaVersion,
		State:              model.OrphanRunQuarantined,
		ProjectID:          in.ProjectID,
		RunID:              in.RunID,
		SourcePath:         sourcePath,
		OriginalSHA256:     result.OriginalSHA256,
		OriginalRunJSONB64: base64.StdEncoding.EncodeToString(raw),
		Actor:              in.Actor,
		Session:            in.Session,
		Reason:             in.Reason,
		HubRevisionBefore:  check.HubRevision,
		CreatedAt:          time.Now().UTC(),
	}
	if err := model.ValidateOrphanRunRecovery(recovery); err != nil {
		return result, err
	}
	receipt := model.OrphanRunRecoveryReceipt{
		SchemaVersion:     model.OrphanRunRecoverySchemaVersion,
		State:             model.OrphanRunQuarantined,
		ReceiptStatus:     model.OrphanReceiptPending,
		ProjectID:         in.ProjectID,
		RunID:             in.RunID,
		SourcePath:        sourcePath,
		OriginalSHA256:    result.OriginalSHA256,
		Actor:             in.Actor,
		Session:           in.Session,
		Reason:            in.Reason,
		HubRevisionBefore: check.HubRevision,
		CreatedAt:         time.Now().UTC(),
	}
	if err := model.ValidateOrphanRunRecoveryReceipt(receipt); err != nil {
		return result, err
	}

	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: quarantine orphan run "+in.RunID, func(worktree string) ([]string, error) {
		currentRaw, err := fsutil.ReadFileBounded(filepath.Join(worktree, filepath.FromSlash(sourcePath)), s.Config.MaxReadBytes)
		if err != nil {
			return nil, err
		}
		if digestBytes(currentRaw) != result.OriginalSHA256 {
			return nil, fmt.Errorf("orphan run content changed before reconciliation")
		}
		if _, err := os.Stat(filepath.Join(worktree, filepath.FromSlash(s.taskPath(in.ProjectID, run.TaskID)))); err == nil {
			return nil, fmt.Errorf("referenced task appeared before reconciliation")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(sourcePath))); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, recoveryPath, recovery); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{sourcePath, recoveryPath, receiptPath}, nil
	})
	if err != nil {
		return result, err
	}

	_, receiptTx, err := s.completeOrphanRecoveryReceipt(ctx, tx.After, receiptPath, receipt)
	if err != nil {
		return result, fmt.Errorf("orphan run quarantined with pending receipt: %w", err)
	}
	after, err := s.StateCheck(ctx)
	if err != nil {
		return result, err
	}
	if !after.Valid {
		return result, fmt.Errorf("orphan reconciliation validation failed: %d issue(s)", len(after.Issues))
	}
	result.Applied = true
	result.Backup = &backup
	result.HubRevisionBefore = check.HubRevision
	result.HubRevisionAfter = tx.After
	result.ReceiptHubRevision = receiptTx.After
	result.ChangedPaths = []string{sourcePath, recoveryPath, receiptPath}
	result.Check = after
	return result, nil
}

func (s *Service) completeOrphanRecoveryReceipt(ctx context.Context, expected, receiptPath string, pending model.OrphanRunRecoveryReceipt) (model.OrphanRunRecoveryReceipt, hub.TransactionResult, error) {
	if pending.ReceiptStatus != model.OrphanReceiptPending {
		return pending, hub.TransactionResult{}, fmt.Errorf("orphan recovery receipt is not pending")
	}
	completed := pending
	completed.ReceiptStatus = model.OrphanReceiptCompleted
	completed.HubRevisionAfter = expected
	if err := model.ValidateOrphanRunRecoveryReceipt(completed); err != nil {
		return pending, hub.TransactionResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: complete orphan run recovery receipt "+pending.RunID, func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, receiptPath, completed); err != nil {
			return nil, err
		}
		return []string{receiptPath}, nil
	})
	if err != nil {
		return pending, hub.TransactionResult{}, err
	}
	return completed, tx, nil
}

func hasExactIssue(check StateCheckResult, code, projectID, runID, taskID string) bool {
	if len(check.Issues) != 1 {
		return false
	}
	issue := check.Issues[0]
	return issue.Code == code && issue.ProjectID == projectID && issue.RunID == runID && issue.TaskID == taskID
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
