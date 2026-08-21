package service

import (
	"context"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func (s *Service) AgentInterrupt(ctx context.Context, in AgentInterruptInput) (AgentInterruptResult, error) {
	if err := validateAgentInterruptInput(in); err != nil {
		return AgentInterruptResult{}, err
	}
	requestDigest, err := agentInterruptRequestDigest(in)
	if err != nil {
		return AgentInterruptResult{}, err
	}
	receiptPath := s.agentInterruptReceiptPath(in.ProjectID, in.OperationID)
	if receipt, found, err := readAgentInterruptReceipt(receiptPath, requestDigest); err != nil {
		return AgentInterruptResult{}, err
	} else if found {
		if receipt.Phase != "interrupt_acknowledged" {
			return receipt.Result, nil
		}
		return s.completeAgentInterruptPrompt(ctx, in, receiptPath, requestDigest, receipt.Result)
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "agent-interrupt-"+agentInterruptLockKey(in))
	if err != nil {
		if receipt, found, readErr := readAgentInterruptReceipt(receiptPath, requestDigest); readErr == nil && found {
			if receipt.Phase == "interrupt_acknowledged" {
				return s.completeAgentInterruptPrompt(ctx, in, receiptPath, requestDigest, receipt.Result)
			}
			return receipt.Result, nil
		}
		now := time.Now().UTC()
		return AgentInterruptResult{
			OperationID:   in.OperationID,
			ProjectID:     in.ProjectID,
			TrainID:       in.TrainID,
			ItemPosition:  in.ItemPosition,
			TaskID:        in.TaskID,
			AttemptNumber: in.AttemptNumber,
			AgentID:       in.AgentID,
			Outcome:       "in_flight",
			StartedAt:     now,
			FinishedAt:    now,
		}, nil
	}
	defer func() { _ = lock.Release() }()
	if receipt, found, err := readAgentInterruptReceipt(receiptPath, requestDigest); err != nil {
		return AgentInterruptResult{}, err
	} else if found {
		if receipt.Phase != "interrupt_acknowledged" {
			return receipt.Result, nil
		}
		return s.completeAgentInterruptPrompt(ctx, in, receiptPath, requestDigest, receipt.Result)
	}

	result := AgentInterruptResult{
		OperationID:   in.OperationID,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		ItemPosition:  in.ItemPosition,
		TaskID:        in.TaskID,
		AttemptNumber: in.AttemptNumber,
		AgentID:       in.AgentID,
		StartedAt:     time.Now().UTC(),
	}
	target, stale, targetErr := s.resolveAgentInterruptTarget(ctx, in)
	if targetErr != nil {
		result.Outcome, result.Error = "stale_execution", targetErr.Error()
		result.FinishedAt = time.Now().UTC()
		return result, s.persistAgentInterruptReceipt(receiptPath, requestDigest, agentInterruptReceipt{
			Phase:  "completed",
			Result: result,
		})
	}
	if stale {
		result.Outcome, result.Error = "stale_execution", "Train Attempt execution identity is no longer current"
		result.FinishedAt = time.Now().UTC()
		return result, s.persistAgentInterruptReceipt(receiptPath, requestDigest, agentInterruptReceipt{
			Phase:  "completed",
			Result: result,
		})
	}
	status, statusErr := s.Airelay.Status(ctx, target.SessionKey)
	if statusErr != nil {
		if in.Message != "" {
			result.PromptOutcome = "not_submitted"
		}
		return s.finishAgentInterrupt(receiptPath, requestDigest, result, "failed", statusErr.Error())
	}
	if status.State == "idle" {
		result.InterruptOutcome = "already_idle"
		if in.Message == "" {
			return s.finishAgentInterrupt(receiptPath, requestDigest, result, "already_idle", "")
		}
		return s.completeAgentInterruptPrompt(ctx, in, receiptPath, requestDigest, result)
	}
	if status.State != "running" && status.State != "waiting" {
		if in.Message != "" {
			result.PromptOutcome = "not_submitted"
		}
		return s.finishAgentInterrupt(receiptPath, requestDigest, result, "failed", "Airelay session is not in an interruptible state")
	}
	interrupt, interruptErr := s.Airelay.Interrupt(ctx, target.SessionKey)
	result.Outcome, result.InterruptOutcome = interrupt.Outcome, interrupt.Outcome
	result.Requested, result.ElapsedMS, result.Reason = interrupt.Requested, interrupt.ElapsedMS, interrupt.Reason
	result.Error = interrupt.Error
	if interruptErr != nil && result.Error == "" {
		result.Error = interruptErr.Error()
	}
	if interrupt.Outcome != "interrupt_acknowledged" || interruptErr != nil || in.Message == "" {
		if in.Message != "" && result.PromptOutcome == "" {
			result.PromptOutcome = "not_submitted"
		}
		return result, s.persistAgentInterruptReceipt(receiptPath, requestDigest, agentInterruptReceipt{
			RequestSHA256: requestDigest,
			Phase:         "completed",
			Result:        result,
		})
	}
	if err := s.persistAgentInterruptReceipt(receiptPath, requestDigest, agentInterruptReceipt{
		RequestSHA256: requestDigest,
		Phase:         "interrupt_acknowledged",
		Result:        result,
	}); err != nil {
		return result, err
	}
	return s.completeAgentInterruptPrompt(ctx, in, receiptPath, requestDigest, result)
}
func (s *Service) completeAgentInterruptPrompt(ctx context.Context, in AgentInterruptInput, receiptPath, requestDigest string, result AgentInterruptResult) (AgentInterruptResult, error) {
	if in.Message == "" {
		return s.finishAgentInterrupt(receiptPath, requestDigest, result, result.Outcome, result.Error)
	}
	if _, stale, err := s.resolveAgentInterruptTarget(ctx, in); err != nil || stale {
		result.Outcome, result.PromptOutcome, result.Error = "turn_changed", "not_submitted", "Train Attempt changed before replacement prompt"
		if err != nil {
			result.Error = err.Error()
		}
		return s.finishAgentInterrupt(receiptPath, requestDigest, result, result.Outcome, result.Error)
	}
	prompt, promptErr := s.AgentPrompt(ctx, in.ProjectID, in.Message)
	if promptErr == nil && prompt.Queued {
		result.PromptOutcome = "queued"
		result.Outcome = "completed"
		return s.finishAgentInterrupt(receiptPath, requestDigest, result, result.Outcome, "")
	} else {
		result.PromptOutcome = "failed"
		if promptErr != nil {
			result.Error = promptErr.Error()
		}
	}
	if !prompt.Queued {
		result.Outcome = "failed"
	}
	return s.finishAgentInterrupt(receiptPath, requestDigest, result, result.Outcome, result.Error)
}
func (s *Service) finishAgentInterrupt(path, requestDigest string, result AgentInterruptResult, outcome, failure string) (AgentInterruptResult, error) {
	result.Outcome = outcome
	if failure != "" {
		result.Error = failure
	}
	result.FinishedAt = time.Now().UTC()
	return result, s.persistAgentInterruptReceipt(path, requestDigest, agentInterruptReceipt{
		RequestSHA256: requestDigest,
		Phase:         "completed",
		Result:        result,
	})
}
