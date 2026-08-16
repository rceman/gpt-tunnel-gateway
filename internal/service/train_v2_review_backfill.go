package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2ReviewBackfillItem struct {
	Position      int    `json:"position"`
	TaskID        string `json:"task_id"`
	AttemptNumber uint64 `json:"attempt_number"`
	ReportPath    string `json:"report_path"`
	ReportSHA256  string `json:"report_sha256"`
	ReviewPath    string `json:"review_path"`
	ReviewedHead  string `json:"reviewed_head"`
}

type TrainV2ReviewBackfillResult struct {
	DryRun          bool                        `json:"dry_run"`
	Applied         bool                        `json:"applied"`
	AlreadyMigrated bool                        `json:"already_migrated"`
	ProjectID       string                      `json:"project_id"`
	TrainID         string                      `json:"train_id"`
	ItemStart       int                         `json:"item_start"`
	ItemEnd         int                         `json:"item_end"`
	HubBefore       string                      `json:"hub_before"`
	HubAfter        string                      `json:"hub_after,omitempty"`
	ReceiptPath     string                      `json:"receipt_path"`
	ChangedPaths    []string                    `json:"changed_paths,omitempty"`
	Items           []TrainV2ReviewBackfillItem `json:"items"`
}

type TrainV2ReviewBackfillReceipt struct {
	OperationID string                       `json:"operation_id"`
	Status      string                       `json:"status"`
	Result      *TrainV2ReviewBackfillResult `json:"result,omitempty"`
	Error       string                       `json:"error,omitempty"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
}

type trainV2ReviewBackfillHubReceipt struct {
	SchemaVersion int                         `json:"schema_version"`
	ProjectID     string                      `json:"project_id"`
	TrainID       string                      `json:"train_id"`
	ItemStart     int                         `json:"item_start"`
	ItemEnd       int                         `json:"item_end"`
	State         string                      `json:"state"`
	HubBefore     string                      `json:"hub_before"`
	HubAfter      string                      `json:"hub_after,omitempty"`
	Items         []TrainV2ReviewBackfillItem `json:"items"`
	Reason        string                      `json:"reason"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

type trainV2ReviewBackfillIdentity struct {
	ProjectID   string `json:"project_id"`
	TrainID     string `json:"train_id"`
	ItemStart   int    `json:"item_start"`
	ItemEnd     int    `json:"item_end"`
	Apply       bool   `json:"apply"`
	HubRevision string `json:"hub_revision,omitempty"`
}

func trainV2ReviewBackfillPath(projectID, trainID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-review-backfill/" + trainID + ".json"
}

func trainV2ReviewBackfillReceipt(operation durableMutationOperation) TrainV2ReviewBackfillReceipt {
	receipt := TrainV2ReviewBackfillReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status == "completed" && len(operation.Result) > 0 {
		var result TrainV2ReviewBackfillResult
		if err := json.Unmarshal(operation.Result, &result); err != nil {
			receipt.Status = "failed"
			receipt.Error = "invalid durable Train review backfill result"
		} else {
			receipt.Result = &result
		}
	}
	return receipt
}

func (s *Service) TrainV2ReviewBackfillAsync(ctx context.Context, in TrainV2ReviewBackfillInput) (TrainV2ReviewBackfillReceipt, error) {
	if err := validateTrainV2ReviewBackfillInput(in); err != nil {
		return TrainV2ReviewBackfillReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutationWithIdentity(ctx, "train-v2-review-backfill", in.ProjectID, in, trainV2ReviewBackfillIdentity{
		ProjectID:   in.ProjectID,
		TrainID:     in.TrainID,
		ItemStart:   in.ItemStart,
		ItemEnd:     in.ItemEnd,
		Apply:       in.Apply,
		HubRevision: s.localHubRevision(ctx),
	})
	if err != nil {
		return TrainV2ReviewBackfillReceipt{}, err
	}
	return trainV2ReviewBackfillReceipt(operation), nil
}

func (s *Service) TrainV2ReviewBackfillOperationStatus(ctx context.Context, operationID string) (TrainV2ReviewBackfillReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2ReviewBackfillReceipt{}, err
	}
	if operation.Kind != "train-v2-review-backfill" {
		return TrainV2ReviewBackfillReceipt{}, fmt.Errorf("operation is not a Train review backfill")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2ReviewBackfillReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2ReviewBackfillReceipt(operation), nil
}

func validateTrainV2ReviewBackfillInput(in TrainV2ReviewBackfillInput) error {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return err
	}
	if in.ItemStart < 0 || in.ItemEnd < in.ItemStart {
		return fmt.Errorf("invalid Train review backfill item range")
	}
	if in.Apply && in.ExpectedHubRevision == "" {
		return fmt.Errorf("Train review backfill apply requires expected_hub_revision")
	}
	return nil
}

func validateBackfillGates(report model.TrainV2AttemptReport) error {
	gates := report.ServerGateResults
	if len(gates) == 0 {
		gates = report.GateResults
	}
	seen := map[string]bool{}
	for _, gate := range gates {
		if gate.ID != model.WorkflowGateFormat && gate.ID != model.WorkflowGateCheck && gate.ID != model.WorkflowGateTest {
			return fmt.Errorf("report contains unexpected gate %q", gate.ID)
		}
		if seen[gate.ID] || gate.ExitCode != 0 {
			return fmt.Errorf("report contains duplicate or failed gate %q", gate.ID)
		}
		seen[gate.ID] = true
	}
	for _, id := range []string{model.WorkflowGateFormat, model.WorkflowGateCheck, model.WorkflowGateTest} {
		if !seen[id] {
			return fmt.Errorf("report is missing gate %q", id)
		}
	}
	return nil
}

func buildTrainV2ReviewBackfillPlan(train model.TrainV2, start, end int, readFile func(string) ([]byte, error)) ([]TrainV2ReviewBackfillItem, error) {
	if start < 0 || end < start || end >= len(train.Items) {
		return nil, fmt.Errorf("Train review backfill range is out of bounds")
	}
	items := make([]TrainV2ReviewBackfillItem, 0, end-start+1)
	for position := start; position <= end; position++ {
		item := train.Items[position]
		if item.Status != model.TrainV2ItemFinalized || item.Proof == nil || item.Review != nil || item.ActiveAttemptNumber != 0 || item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
			return nil, fmt.Errorf("Train item %q is not an unreviewed finalized proof", item.TaskID)
		}
		attempt := item.Attempts[item.SuccessfulAttemptNumber-1]
		reportPath := trainV2AttemptReportPath(train.ProjectID, train.ID, position, item.SuccessfulAttemptNumber)
		if item.Proof.ReportID != reportPath || attempt.ReportID != reportPath || attempt.Status != model.TrainV2AttemptSucceeded || attempt.ReviewID != "" {
			return nil, fmt.Errorf("Train item %q has inconsistent report identity", item.TaskID)
		}
		raw, err := readFile(reportPath)
		if err != nil {
			return nil, fmt.Errorf("read Attempt report %s: %w", reportPath, err)
		}
		var report model.TrainV2AttemptReport
		if err := decodeStrict(raw, &report); err != nil {
			return nil, fmt.Errorf("decode Attempt report %s: %w", reportPath, err)
		}
		if err := model.ValidateTrainV2AttemptReport(report); err != nil || report.ProjectID != train.ProjectID || report.TrainID != train.ID || report.TaskID != item.TaskID || report.ItemPosition != position || report.AttemptNumber != item.SuccessfulAttemptNumber || report.Status != "succeeded" || !report.Repository.WorktreeClean || report.Repository.Head != item.Proof.ImplementationSHA || report.Repository.Head != item.Proof.CheckpointHead {
			return nil, fmt.Errorf("Attempt report identity/proof mismatch for %s", item.TaskID)
		}
		if err := validateBackfillGates(report); err != nil {
			return nil, fmt.Errorf("Attempt report gates for %s: %w", item.TaskID, err)
		}
		reviewPath := trainV2AttemptReviewPath(train.ProjectID, train.ID, position, item.SuccessfulAttemptNumber)
		if _, reviewErr := readFile(reviewPath); reviewErr == nil {
			return nil, fmt.Errorf("conflicting existing review for %s", item.TaskID)
		} else if !IsNotFound(reviewErr) {
			return nil, fmt.Errorf("read existing review %s: %w", reviewPath, reviewErr)
		}
		items = append(items, TrainV2ReviewBackfillItem{
			Position:      position,
			TaskID:        item.TaskID,
			AttemptNumber: item.SuccessfulAttemptNumber,
			ReportPath:    reportPath,
			ReportSHA256:  digestBytes(raw),
			ReviewPath:    reviewPath,
			ReviewedHead:  item.Proof.ImplementationSHA,
		})
	}
	return items, nil
}

func (s *Service) TrainV2ReviewBackfill(ctx context.Context, in TrainV2ReviewBackfillInput) (TrainV2ReviewBackfillResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	if err := validateTrainV2ReviewBackfillInput(in); err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	checkRevision, err := s.hubRevision(ctx)
	if err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != checkRevision {
		return TrainV2ReviewBackfillResult{}, fmt.Errorf("HUB_REVISION_CONFLICT: expected %s, got %s", in.ExpectedHubRevision, checkRevision)
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	receiptPath := trainV2ReviewBackfillPath(in.ProjectID, in.TrainID)
	result := TrainV2ReviewBackfillResult{
		DryRun:      !in.Apply,
		ProjectID:   in.ProjectID,
		TrainID:     in.TrainID,
		ItemStart:   in.ItemStart,
		ItemEnd:     in.ItemEnd,
		HubBefore:   checkRevision,
		ReceiptPath: receiptPath,
	}
	if raw, readErr := s.Hub.ReadFile(ctx, receiptPath); readErr == nil {
		var receipt trainV2ReviewBackfillHubReceipt
		if err := decodeStrict(raw, &receipt); err != nil || receipt.ProjectID != in.ProjectID || receipt.TrainID != in.TrainID || receipt.ItemStart != in.ItemStart || receipt.ItemEnd != in.ItemEnd {
			return result, fmt.Errorf("invalid Train review backfill receipt")
		}
		if err := s.validateAppliedReviewBackfill(ctx, train, receipt.Items); err != nil {
			return result, err
		}
		if receipt.State == "completed" {
			result.AlreadyMigrated, result.Applied, result.HubAfter, result.Items = true, true, receipt.HubAfter, append([]TrainV2ReviewBackfillItem{}, receipt.Items...)
			return result, nil
		}
		if receipt.State != "pending" || !in.Apply {
			return result, fmt.Errorf("invalid pending Train review backfill state")
		}
		result.AlreadyMigrated, result.Applied, result.HubAfter, result.Items = true, true, checkRevision, append([]TrainV2ReviewBackfillItem{}, receipt.Items...)
		return result, nil
	} else if !IsNotFound(readErr) {
		return result, readErr
	}
	items, err := buildTrainV2ReviewBackfillPlan(train, in.ItemStart, in.ItemEnd, func(path string) ([]byte, error) { return s.Hub.ReadFile(ctx, path) })
	if err != nil {
		return result, err
	}
	result.Items = items
	if !in.Apply {
		return result, nil
	}
	now := nowUTC()
	receipt := trainV2ReviewBackfillHubReceipt{
		SchemaVersion: model.TrainV2AttemptSchemaVersion,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		ItemStart:     in.ItemStart,
		ItemEnd:       in.ItemEnd,
		State:         "pending",
		HubBefore:     checkRevision,
		Items:         items,
		Reason:        "backfill accepted reviews for immutable pre-review Train attempts",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	tx, err := s.Hub.Transact(ctx, checkRevision, "gateway: backfill Train-v2 reviews", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision {
			return nil, fmt.Errorf("Train changed before review backfill")
		}
		planned, err := buildTrainV2ReviewBackfillPlan(latest, in.ItemStart, in.ItemEnd, func(path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
		})
		if err != nil {
			return nil, err
		}
		if err := applyTrainV2ReviewBackfill(&latest, planned, now); err != nil {
			return nil, err
		}
		receipt.Items = planned
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		paths := []string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}
		for _, item := range planned {
			review := model.TrainV2AttemptReview{SchemaVersion: model.TrainV2AttemptSchemaVersion, ID: fmt.Sprintf("%s-ITEM%d-ATTEMPT%d-REVIEW", in.TrainID, item.Position, item.AttemptNumber), TrainID: in.TrainID, TaskID: item.TaskID, ItemPosition: item.Position, AttemptNumber: item.AttemptNumber, Outcome: model.ReviewOutcomeAccepted, ReviewedHead: item.ReviewedHead, Findings: []model.ReviewFinding{}, ScopeCoverage: []model.ReviewScopeCoverage{}, ReviewedAt: now}
			if err := hub.WriteJSON(worktree, item.ReviewPath, review); err != nil {
				return nil, err
			}
			paths = append(paths, item.ReviewPath)
		}
		return paths, nil
	})
	if err != nil {
		return result, err
	}
	receipt.State, receipt.HubAfter, receipt.UpdatedAt = "completed", tx.After, nowUTC()
	if _, err := s.Hub.Transact(ctx, tx.After, "gateway: complete Train-v2 review backfill", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{receiptPath}, nil
	}); err != nil {
		return result, err
	}
	result.Applied, result.HubAfter, result.ChangedPaths = true, receipt.HubAfter, append([]string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}, reviewPaths(items)...)
	return result, nil
}

func applyTrainV2ReviewBackfill(train *model.TrainV2, items []TrainV2ReviewBackfillItem, now time.Time) error {
	for _, planned := range items {
		item := &train.Items[planned.Position]
		item.Attempts[planned.AttemptNumber-1].ReviewID = fmt.Sprintf("%s-ITEM%d-ATTEMPT%d-REVIEW", train.ID, planned.Position, planned.AttemptNumber)
		item.Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: item.Attempts[planned.AttemptNumber-1].ReviewID, ReviewedAt: now}
		item.Status = model.TrainV2ItemReviewed
	}
	train.Revision++
	train.UpdatedAt = now
	return model.ValidateTrainV2(*train)
}

func (s *Service) validateAppliedReviewBackfill(ctx context.Context, train model.TrainV2, items []TrainV2ReviewBackfillItem) error {
	for _, planned := range items {
		if planned.Position < 0 || planned.Position >= len(train.Items) {
			return fmt.Errorf("Train review backfill receipt no longer matches item %d", planned.Position)
		}
		item := train.Items[planned.Position]
		reviewID := fmt.Sprintf("%s-ITEM%d-ATTEMPT%d-REVIEW", train.ID, planned.Position, planned.AttemptNumber)
		if item.Review == nil || item.Review.Outcome != model.ReviewOutcomeAccepted || item.Review.ReportID != reviewID || item.SuccessfulAttemptNumber != planned.AttemptNumber || len(item.Attempts) < int(planned.AttemptNumber) || item.Attempts[planned.AttemptNumber-1].ReviewID != reviewID {
			return fmt.Errorf("Train review backfill receipt no longer matches item %d", planned.Position)
		}
		raw, err := s.Hub.ReadFile(ctx, planned.ReportPath)
		if err != nil || digestBytes(raw) != planned.ReportSHA256 {
			return fmt.Errorf("Train review backfill report digest changed for item %d", planned.Position)
		}
		var report model.TrainV2AttemptReport
		if err := decodeStrict(raw, &report); err != nil || report.TrainID != train.ID || report.TaskID != planned.TaskID || report.ItemPosition != planned.Position || report.AttemptNumber != planned.AttemptNumber || report.Repository.Head != planned.ReviewedHead || report.Status != "succeeded" {
			return fmt.Errorf("Train review backfill report no longer matches item %d", planned.Position)
		}
		reviewRaw, err := s.Hub.ReadFile(ctx, planned.ReviewPath)
		if err != nil {
			return fmt.Errorf("Train review backfill review is missing for item %d", planned.Position)
		}
		var review model.TrainV2AttemptReview
		if err := decodeStrict(reviewRaw, &review); err != nil || review.ID != reviewID || review.TrainID != train.ID || review.TaskID != planned.TaskID || review.ItemPosition != planned.Position || review.AttemptNumber != planned.AttemptNumber || review.Outcome != model.ReviewOutcomeAccepted || review.ReviewedHead != planned.ReviewedHead {
			return fmt.Errorf("Train review backfill review no longer matches item %d", planned.Position)
		}
	}
	return nil
}

func reviewPaths(items []TrainV2ReviewBackfillItem) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.ReviewPath)
	}
	return paths
}
