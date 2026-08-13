package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type AgentInterruptInput struct {
	OperationID   string `json:"operation_id"`
	ProjectID     string `json:"project_id"`
	TrainID       string `json:"train_id"`
	ItemPosition  int    `json:"item_position"`
	TaskID        string `json:"task_id"`
	AttemptNumber uint64 `json:"attempt_number"`
	AgentID       string `json:"agent_id"`
	Message       string `json:"message,omitempty"`
}

type AgentInterruptResult struct {
	OperationID      string    `json:"operation_id"`
	ProjectID        string    `json:"project_id"`
	TrainID          string    `json:"train_id"`
	ItemPosition     int       `json:"item_position"`
	TaskID           string    `json:"task_id"`
	AttemptNumber    uint64    `json:"attempt_number"`
	AgentID          string    `json:"agent_id"`
	Outcome          string    `json:"outcome"`
	InterruptOutcome string    `json:"interrupt_outcome,omitempty"`
	PromptOutcome    string    `json:"prompt_outcome,omitempty"`
	Requested        bool      `json:"requested"`
	PromptDelivered  bool      `json:"prompt_delivered,omitempty"`
	ElapsedMS        int       `json:"elapsed_ms,omitempty"`
	Error            string    `json:"error,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
}

type agentInterruptReceipt struct {
	RequestSHA256 string               `json:"request_sha256"`
	Phase         string               `json:"phase"`
	Result        AgentInterruptResult `json:"result"`
}

const agentInterruptReceiptLimit = 1 << 20

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
	result.PromptDelivered = prompt.Delivered && promptErr == nil
	if result.PromptDelivered {
		result.PromptOutcome = "delivered"
	} else {
		result.PromptOutcome = "failed"
		if promptErr != nil {
			result.Error = promptErr.Error()
		} else if prompt.Error != "" {
			result.Error = prompt.Error
		}
	}
	if !result.PromptDelivered {
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

type interruptTarget struct{ SessionKey string }

func (s *Service) resolveAgentInterruptTarget(ctx context.Context, in AgentInterruptInput) (interruptTarget, bool, error) {
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return interruptTarget{}, true, err
	}
	if train.ProjectID != in.ProjectID || in.ItemPosition < 0 || in.ItemPosition >= len(train.Items) {
		return interruptTarget{}, true, nil
	}
	item := train.Items[in.ItemPosition]
	if item.TaskID != in.TaskID || in.AttemptNumber == 0 || in.AttemptNumber > uint64(len(item.Attempts)) {
		return interruptTarget{}, true, nil
	}
	attempt := item.Attempts[in.AttemptNumber-1]
	if attempt.Number != in.AttemptNumber || attempt.AgentID != in.AgentID || attempt.Status != model.TrainV2AttemptRunning {
		return interruptTarget{}, true, nil
	}
	var start model.TrainV2StartRecord
	startPath := "gpt-tunnel/v1/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil || start.CurrentItemPosition != in.ItemPosition || start.CurrentAttemptNumber != in.AttemptNumber || start.CurrentTaskID != in.TaskID {
		return interruptTarget{}, true, nil
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil || runtime.ItemPosition != in.ItemPosition || runtime.TaskID != in.TaskID || runtime.AttemptNumber != in.AttemptNumber || runtime.AgentID != in.AgentID || runtime.SessionKey != attempt.AirelaySessionKey {
		return interruptTarget{}, true, nil
	}
	agent, err := s.AgentRead(ctx, in.ProjectID, in.AgentID)
	if err != nil {
		return interruptTarget{}, true, nil
	}
	agents, err := s.AgentList(ctx, in.ProjectID)
	if err != nil {
		return interruptTarget{}, true, nil
	}
	binding, ok := s.resolveLocalAgentBinding(in.ProjectID, agent, agents)
	if !ok || binding.SessionKey != attempt.AirelaySessionKey {
		return interruptTarget{}, true, nil
	}
	return interruptTarget{SessionKey: binding.SessionKey}, false, nil
}

func validateAgentInterruptInput(in AgentInterruptInput) error {
	if model.ValidateObjectIdentifier(in.OperationID) != nil || model.ValidateProjectIdentifier(in.ProjectID) != nil || model.ValidateObjectIdentifier(in.TrainID) != nil || model.ValidateCanonicalTaskID(in.TaskID) != nil || model.ValidateObjectIdentifier(in.AgentID) != nil || in.ItemPosition < 0 || in.AttemptNumber < 1 {
		return fmt.Errorf("invalid agent interrupt execution identity")
	}
	if len(in.Message) > 256 || strings.ContainsRune(in.Message, 0) {
		return fmt.Errorf("invalid replacement prompt")
	}
	return nil
}

func agentInterruptRequestDigest(in AgentInterruptInput) (string, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func agentInterruptLockKey(in AgentInterruptInput) string {
	digest, _ := agentInterruptRequestDigest(in)
	return digest[:16]
}

func (s *Service) agentInterruptReceiptPath(projectID, operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return filepath.Join(s.Config.StateDir, "agent-interrupts", projectID, hex.EncodeToString(digest[:])+".json")
}

func readAgentInterruptReceipt(path, requestDigest string) (agentInterruptReceipt, bool, error) {
	var receipt agentInterruptReceipt
	if err := fsutil.ReadJSONBounded(path, agentInterruptReceiptLimit, &receipt); err != nil {
		if os.IsNotExist(err) {
			return agentInterruptReceipt{}, false, nil
		}
		return agentInterruptReceipt{}, false, err
	}
	if receipt.RequestSHA256 != requestDigest {
		return agentInterruptReceipt{}, false, fmt.Errorf("agent interrupt operation_id is bound to different execution identity")
	}
	return receipt, true, nil
}

func (s *Service) persistAgentInterruptReceipt(path, requestDigest string, receipt agentInterruptReceipt) error {
	receipt.RequestSHA256 = requestDigest
	return fsutil.WriteJSONAtomic(path, receipt, 0o600)
}
