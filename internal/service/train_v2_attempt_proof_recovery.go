package service

import (
	"context"
	"fmt"
	"reflect"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// applyTrainV2AttemptProof is the shared state transition for normal Attempt
// finalization and recovery. It never changes an accepted review.
func applyTrainV2AttemptProof(item *model.TrainV2Item, proof model.TrainV2ImplementationProof) error {
	if item == nil || (item.Status != model.TrainV2ItemFinalized && item.Status != model.TrainV2ItemReviewed) {
		return fmt.Errorf("Train item is not finalized or reviewed")
	}
	if item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
		return fmt.Errorf("Train item has no successful Attempt")
	}
	attempt := &item.Attempts[item.SuccessfulAttemptNumber-1]
	if attempt.Status != model.TrainV2AttemptSucceeded {
		return fmt.Errorf("Train item Attempt is not successfully finalized")
	}
	if item.Status == model.TrainV2ItemReviewed {
		if item.Review == nil || item.Review.Outcome != model.ReviewOutcomeAccepted || item.Review.ReportID == "" || attempt.ReviewID != item.Review.ReportID {
			return fmt.Errorf("reviewed Train item has invalid accepted review")
		}
	}
	if err := model.ValidateServerGateEvidence(proof.GateResults); err != nil {
		return err
	}
	if model.ValidateCommitSHA(proof.CheckpointHead) != nil || model.ValidateCommitSHA(proof.ImplementationSHA) != nil || proof.ReportID == "" || proof.RecordedAt.IsZero() {
		return fmt.Errorf("invalid Train item implementation proof")
	}
	if item.Proof != nil {
		if !reflect.DeepEqual(*item.Proof, proof) {
			return fmt.Errorf("Train item already has different implementation proof")
		}
		return nil
	}
	item.Proof = &proof
	attempt.ReportID = proof.ReportID
	return nil
}

type TrainV2AttemptProofRecoveryInput struct {
	ProjectID     string `json:"project_id"`
	TrainID       string `json:"train_id"`
	ItemPosition  int    `json:"item_position"`
	AttemptNumber uint64 `json:"attempt_number"`
	WriteOptions
}

type TrainV2AttemptProofRecoveryResult struct {
	Report model.TrainV2AttemptReport `json:"report"`
	Hub    hub.TransactionResult      `json:"hub"`
}

// TrainV2AttemptProofRecover restores only missing immutable proof for an
// already succeeded Attempt. Accepted review state is preserved exactly.
func (s *Service) TrainV2AttemptProofRecover(ctx context.Context, in TrainV2AttemptProofRecoveryInput) (TrainV2AttemptProofRecoveryResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2AttemptProofRecoveryResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil || in.ItemPosition < 0 || in.AttemptNumber == 0 {
		return TrainV2AttemptProofRecoveryResult{}, fmt.Errorf("invalid Train-v2 Attempt proof recovery identity")
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2AttemptProofRecoveryResult{}, err
	}
	if in.ItemPosition >= len(train.Items) {
		return TrainV2AttemptProofRecoveryResult{}, fmt.Errorf("Train item position is out of range")
	}
	item := train.Items[in.ItemPosition]
	if item.ActiveAttemptNumber != 0 || item.SuccessfulAttemptNumber != in.AttemptNumber || in.AttemptNumber > uint64(len(item.Attempts)) {
		return TrainV2AttemptProofRecoveryResult{}, fmt.Errorf("Attempt is not the exact successful recovery target")
	}
	attempt := item.Attempts[in.AttemptNumber-1]
	if attempt.Status != model.TrainV2AttemptSucceeded || item.Proof != nil {
		return TrainV2AttemptProofRecoveryResult{}, fmt.Errorf("Attempt proof is not recoverable")
	}
	if item.Status != model.TrainV2ItemFinalized && item.Status != model.TrainV2ItemReviewed {
		return TrainV2AttemptProofRecoveryResult{}, fmt.Errorf("Train item is not finalized or reviewed")
	}
	if item.Status == model.TrainV2ItemReviewed && (item.Review == nil || item.Review.Outcome != model.ReviewOutcomeAccepted || item.Review.ReportID == "" || attempt.ReviewID != item.Review.ReportID) {
		return TrainV2AttemptProofRecoveryResult{}, fmt.Errorf("reviewed Train item has invalid accepted review")
	}
	evidence, err := s.sharedTrainEvidence()
	if err != nil {
		return TrainV2AttemptProofRecoveryResult{}, err
	}
	report, err := evidence.ReadAttemptReport(in.TrainID, item.TaskID, in.AttemptNumber)
	if err != nil {
		return TrainV2AttemptProofRecoveryResult{}, err
	}
	reportPath := hub.TrainAttemptReportPath(in.ProjectID, in.TrainID, item.Position, in.AttemptNumber)
	if err := validateProofRecoveryReport(report, in.ProjectID, train, item, reportPath); err != nil {
		return TrainV2AttemptProofRecoveryResult{}, fmt.Errorf("proof recovery rejected: %w", err)
	}
	proof := model.TrainV2ImplementationProof{CheckpointHead: report.Repository.Head, ImplementationSHA: report.Repository.Head, ReportID: reportPath, GateResults: append([]model.CompletionGateResult{}, report.ServerGateResults...), RecordedAt: report.FinishedAt}
	if err := applyTrainV2AttemptProof(&item, proof); err != nil {
		return TrainV2AttemptProofRecoveryResult{}, err
	}
	return s.recoverTrainV2AttemptProof(ctx, in, train, item, report, reportPath, proof)
}

func (s *Service) recoverTrainV2AttemptProof(ctx context.Context, in TrainV2AttemptProofRecoveryInput, train model.TrainV2, item model.TrainV2Item, report model.TrainV2AttemptReport, reportPath string, proof model.TrainV2ImplementationProof) (TrainV2AttemptProofRecoveryResult, error) {
	expected := in.ExpectedHubRevision
	if expected == "" {
		var err error
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2AttemptProofRecoveryResult{}, err
		}
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: recover Train-v2 Attempt implementation proof", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || in.ItemPosition >= len(latest.Items) {
			return nil, fmt.Errorf("Train changed before proof recovery")
		}
		latestItem := latest.Items[in.ItemPosition]
		if latestItem.TaskID != item.TaskID || latestItem.SuccessfulAttemptNumber != in.AttemptNumber || latestItem.Proof != nil || !reflect.DeepEqual(latestItem.Review, item.Review) {
			return nil, fmt.Errorf("Train Attempt proof recovery identity changed")
		}
		if latestItem.Status != item.Status {
			return nil, fmt.Errorf("Train item status changed before proof recovery")
		}
		var latestReport model.TrainV2AttemptReport
		if err := readWorktreeJSON(worktree, reportPath, &latestReport); err != nil {
			return nil, err
		}
		if err := validateProofRecoveryReport(latestReport, in.ProjectID, latest, latestItem, reportPath); err != nil || !reflect.DeepEqual(latestReport, report) {
			return nil, fmt.Errorf("Attempt report changed before proof recovery")
		}
		if err := applyTrainV2AttemptProof(&latestItem, proof); err != nil {
			return nil, err
		}
		latest.Items[in.ItemPosition] = latestItem
		latest.Revision++
		latest.UpdatedAt = proof.RecordedAt
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		trainPath := s.trainV2Path(in.ProjectID, in.TrainID)
		if err := hub.WriteJSON(worktree, trainPath, latest); err != nil {
			return nil, err
		}
		return []string{trainPath}, nil
	})
	if err != nil {
		return TrainV2AttemptProofRecoveryResult{}, err
	}
	return TrainV2AttemptProofRecoveryResult{
		Report: report,
		Hub:    tx,
	}, nil
}
