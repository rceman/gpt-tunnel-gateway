package service

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
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
func (s *Service) recoverFinalizedTaskProof(ctx context.Context, projectID string, train model.TrainV2, item model.TrainV2Item, report model.TrainV2AttemptReport, reportPath string) (model.TrainV2AttemptReport, error) {
	if err := validateProofRecoveryReport(report, projectID, train, item, reportPath); err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery rejected: %w", err)
	}
	project, err := s.projectConfig(projectID)
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + train.ID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery start record: %w", err)
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
	if err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery runtime: %w", err)
	}
	project.Root = runtime.WorktreePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	if !clean || branch != start.LaneBranch || branch != report.Repository.Branch {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery lane identity is invalid")
	}
	ancestor, err := s.Git.IsAncestor(ctx, project.Root, report.Repository.Head, head)
	if err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery checkpoint lookup: %w", err)
	}
	if !ancestor {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery checkpoint is not an ancestor of the current lane head")
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	proof := model.TrainV2ImplementationProof{CheckpointHead: report.Repository.Head, ImplementationSHA: report.Repository.Head, ReportID: reportPath, GateResults: append([]model.CompletionGateResult{}, report.ServerGateResults...), RecordedAt: report.FinishedAt}
	_, err = s.Hub.Transact(ctx, expected, "gateway: recover Train Attempt implementation proof", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(projectID, train.ID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || item.Position >= len(latest.Items) {
			return nil, fmt.Errorf("Train changed before proof recovery")
		}
		latestItem := latest.Items[item.Position]
		if latestItem.TaskID != item.TaskID || latestItem.Status != model.TrainV2ItemFinalized || latestItem.SuccessfulAttemptNumber != item.SuccessfulAttemptNumber || latestItem.Proof != nil {
			return nil, fmt.Errorf("Train Attempt proof recovery identity changed")
		}
		var latestReport model.TrainV2AttemptReport
		if err := readWorktreeJSON(worktree, reportPath, &latestReport); err != nil {
			return nil, err
		}
		if err := validateProofRecoveryReport(latestReport, projectID, latest, latestItem, reportPath); err != nil || !reflect.DeepEqual(latestReport, report) {
			return nil, fmt.Errorf("Attempt report changed before proof recovery")
		}
		latestItem.Proof = &proof
		latest.Items[item.Position] = latestItem
		latest.Revision++
		latest.UpdatedAt = time.Now().UTC()
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		trainPath := s.trainV2Path(projectID, train.ID)
		if err := hub.WriteJSON(worktree, trainPath, latest); err != nil {
			return nil, err
		}
		return []string{trainPath}, nil
	})
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	return report, nil
}
