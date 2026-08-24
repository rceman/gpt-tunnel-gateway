package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type TrainV2FullProofResult struct {
	CandidateHead string                       `json:"candidate_head"`
	GateResults   []model.CompletionGateResult `json:"gate_results"`
	Hub           hub.TransactionResult        `json:"hub"`
}

type TrainV2FullProofReceipt struct {
	OperationID string                  `json:"operation_id"`
	Status      string                  `json:"status"`
	Result      *TrainV2FullProofResult `json:"result,omitempty"`
	Error       string                  `json:"error,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

type trainV2FullProofIdentity struct {
	ProjectID   string `json:"project_id"`
	TrainID     string `json:"train_id"`
	HubRevision string `json:"hub_revision,omitempty"`
}

func trainV2FullProofReceipt(operation durableMutationOperation) TrainV2FullProofReceipt {
	receipt := TrainV2FullProofReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result TrainV2FullProofResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Train full-proof result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) TrainV2FullProofAsync(ctx context.Context, in TrainV2FullProofInput) (TrainV2FullProofReceipt, error) {
	if in.ProjectID == "" {
		return TrainV2FullProofReceipt{}, fmt.Errorf("project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutationWithIdentity(ctx, "train-v2-full-proof", in.ProjectID, in, trainV2FullProofIdentity{
		ProjectID:   in.ProjectID,
		TrainID:     in.TrainID,
		HubRevision: s.localHubRevision(ctx),
	})
	if err != nil {
		return TrainV2FullProofReceipt{}, err
	}
	return trainV2FullProofReceipt(operation), nil
}

func (s *Service) TrainV2FullProofOperationStatus(ctx context.Context, operationID string) (TrainV2FullProofReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2FullProofReceipt{}, err
	}
	if operation.Kind != "train-v2-full-proof" {
		return TrainV2FullProofReceipt{}, fmt.Errorf("operation is not a Train full-proof mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2FullProofReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2FullProofReceipt(operation), nil
}

func trainV2FullProofCandidate(train model.TrainV2) (string, error) {
	var candidate string
	for _, item := range train.Items {
		if item.Status != model.TrainV2ItemReviewed || item.Proof == nil || item.Review == nil || item.Review.Outcome != model.ReviewOutcomeAccepted {
			return "", fmt.Errorf("Train item %q is not fully reviewed and proved", item.TaskID)
		}
		if item.SuccessfulAttemptNumber == 0 || item.ActiveAttemptNumber != 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
			return "", fmt.Errorf("Train item %q has inconsistent Attempt evidence", item.TaskID)
		}
		attempt := item.Attempts[item.SuccessfulAttemptNumber-1]
		if attempt.Status != model.TrainV2AttemptSucceeded || attempt.FinishedAt == nil || attempt.ReviewID == "" || attempt.ReviewID != item.Review.ReportID {
			return "", fmt.Errorf("Train item %q has inconsistent successful Attempt evidence", item.TaskID)
		}
		candidate = item.Proof.ImplementationSHA
	}
	if candidate == "" || model.ValidateCommitSHA(candidate) != nil {
		return "", fmt.Errorf("Train has no valid full-proof candidate")
	}
	return candidate, nil
}

func validateTrainV2FullProofAncestry(train model.TrainV2, candidate string, isAncestor func(string, string) (bool, error)) error {
	for _, item := range train.Items {
		ancestor, err := isAncestor(item.Proof.ImplementationSHA, candidate)
		if err != nil {
			return err
		}
		if !ancestor {
			return fmt.Errorf("Train item %q proof is not an ancestor of the terminal candidate", item.TaskID)
		}
	}
	return nil
}

func (s *Service) TrainV2FullProof(ctx context.Context, in TrainV2FullProofInput) (TrainV2FullProofResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2FullProofResult{}, err
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2FullProofResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2FullProofResult{}, err
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2FullProofResult{}, err
	}
	if train.FullProof != nil {
		if train.Status != model.TrainV2ReadyForIntegration {
			return TrainV2FullProofResult{}, fmt.Errorf("Train has inconsistent existing full proof state")
		}
		return TrainV2FullProofResult{
			CandidateHead: train.FullProof.CandidateHead,
			GateResults:   append([]model.CompletionGateResult{}, train.FullProof.GateResults...),
		}, nil
	}
	candidate, err := trainV2FullProofCandidate(train)
	if err != nil {
		return TrainV2FullProofResult{}, err
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return TrainV2FullProofResult{}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return TrainV2FullProofResult{}, fmt.Errorf("read Train start: %w", err)
	}
	if err := model.ValidateTrainV2StartRecord(start); err != nil {
		return TrainV2FullProofResult{}, err
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2FullProofResult{}, fmt.Errorf("read Train runtime: %w", err)
	}
	lane := project
	lane.Root = runtime.WorktreePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || branch != start.LaneBranch || head != candidate {
		return TrainV2FullProofResult{}, fmt.Errorf("Train lane is not the exact clean full-proof candidate")
	}
	if err := validateTrainV2FullProofAncestry(train, candidate, func(ancestor, descendant string) (bool, error) {
		return s.Git.IsAncestor(ctx, lane.Root, ancestor, descendant)
	}); err != nil {
		return TrainV2FullProofResult{}, err
	}
	gates, err := s.executeTrainGatesWithScopedFormat(ctx, in.ProjectID, lane, start.BaseRevision, candidate)
	if err != nil {
		return TrainV2FullProofResult{}, err
	}
	updated, err := trainv2.RecordFullProof(train, candidate, gates, time.Now().UTC())
	if err != nil {
		return TrainV2FullProofResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2FullProofResult{}, err
		}
	}
	var tx hub.TransactionResult
	tx, err = s.Hub.Transact(ctx, expected, "gateway: record Train full proof", func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &current); err != nil {
			return nil, err
		}
		if current.Revision != train.Revision || current.FullProof != nil {
			return nil, fmt.Errorf("Train changed before full-proof persistence")
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), updated); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(in.ProjectID, in.TrainID)}, nil
	})
	if err != nil {
		return TrainV2FullProofResult{}, err
	}
	return TrainV2FullProofResult{
		CandidateHead: candidate,
		GateResults:   gates,
		Hub:           tx,
	}, nil
}
