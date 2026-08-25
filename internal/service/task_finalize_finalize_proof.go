package service

import (
	"fmt"
	"reflect"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func validateProofRecoveryReport(report model.TrainV2AttemptReport, projectID string, train model.TrainV2, item model.TrainV2Item, path string) error {
	if err := model.ValidateTrainV2AttemptReport(report); err != nil {
		return err
	}
	if report.ProjectID != projectID || report.TrainID != train.ID || report.TaskID != item.TaskID || report.ItemPosition != item.Position || report.AttemptNumber != item.SuccessfulAttemptNumber || report.Status != "succeeded" {
		return fmt.Errorf("Attempt report identity or status mismatch")
	}
	if item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) || item.Attempts[item.SuccessfulAttemptNumber-1].Status != model.TrainV2AttemptSucceeded {
		return fmt.Errorf("successful Attempt is not the exact finalized item Attempt")
	}
	if report.Repository.Branch == "" || model.ValidateBranch(report.Repository.Branch) != nil || !report.Repository.WorktreeClean || !report.Repository.BaseAncestor || report.Repository.DiffScope == "" || model.ValidateCommitSHA(report.Repository.Head) != nil {
		return fmt.Errorf("Attempt report repository proof is invalid")
	}
	if len(report.ServerGateResults) == 0 {
		return fmt.Errorf("Attempt report is missing server-owned gate evidence")
	}
	if err := model.ValidateServerGateEvidence(report.ServerGateResults); err != nil {
		return err
	}
	for _, gate := range report.ServerGateResults {
		if gate.ExitCode != 0 {
			return fmt.Errorf("Attempt report contains failed server gate %s", gate.ID)
		}
	}
	if path == "" {
		return fmt.Errorf("Attempt report path is required")
	}
	return nil
}

func validateStoredTrainItemProof(report model.TrainV2AttemptReport, train model.TrainV2, item model.TrainV2Item, path, taskID string) error {
	if err := validateProofRecoveryReport(report, train.ProjectID, train, item, path); err != nil || report.TaskID != taskID || item.Proof == nil {
		return fmt.Errorf("invalid stored Attempt proof report")
	}
	if item.Proof.ReportID != path || item.Proof.CheckpointHead != report.Repository.Head || item.Proof.ImplementationSHA != report.Repository.Head || !reflect.DeepEqual(item.Proof.GateResults, report.ServerGateResults) {
		return fmt.Errorf("stored implementation proof does not match Attempt report")
	}
	return nil
}
