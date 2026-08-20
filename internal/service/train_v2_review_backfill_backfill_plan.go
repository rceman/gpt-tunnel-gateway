package service

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
