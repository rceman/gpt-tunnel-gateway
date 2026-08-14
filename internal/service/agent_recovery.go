package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// AgentRecover reconciles one durable running Attempt with its host-local
// execution binding. It never creates an Attempt or accepts a packet path or
// prompt from the caller.
func (s *Service) AgentRecover(ctx context.Context, in AgentRecoverInput) (AgentRecoveryResult, error) {
	result := AgentRecoveryResult{
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		ItemPosition:  in.ItemPosition,
		TaskID:        in.TaskID,
		AttemptNumber: in.AttemptNumber,
		AgentID:       in.AgentID,
		Outcome:       "blocked",
		Phase:         "validate",
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		result.Reason = err.Error()
		return result, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil || in.ItemPosition < 0 || in.AttemptNumber == 0 || model.ValidateCanonicalTaskID(in.TaskID) != nil || model.ValidateObjectIdentifier(in.AgentID) != nil {
		result.Reason = "invalid exact Train Attempt identity"
		return result, fmt.Errorf("invalid exact Train Attempt identity")
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		result.Reason, result.Phase = err.Error(), "train_read"
		return result, err
	}
	if in.ItemPosition >= len(train.Items) || train.Items[in.ItemPosition].TaskID != in.TaskID {
		return recoveryBlock(result, "train/item identity mismatch")
	}
	item := train.Items[in.ItemPosition]
	if in.AttemptNumber > uint64(len(item.Attempts)) || item.Attempts[in.AttemptNumber-1].Number != in.AttemptNumber || item.Attempts[in.AttemptNumber-1].AgentID != in.AgentID {
		return recoveryBlock(result, "Train Attempt identity mismatch")
	}
	attempt := item.Attempts[in.AttemptNumber-1]
	result.AttemptStatus = attempt.Status
	if attempt.Status != model.TrainV2AttemptRunning {
		result.Outcome, result.Phase, result.RecoveryEvent = "already_completed", "attempt_read", "attempt_not_running"
		return result, nil
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return recoveryBlockAt(result, "runtime_read", err.Error())
	}
	if runtime.ItemPosition != in.ItemPosition || runtime.TaskID != in.TaskID || runtime.AttemptNumber != in.AttemptNumber {
		return recoveryBlockAt(result, "runtime_identity", "host-local runtime binding does not match the exact Attempt")
	}
	agent, err := s.AgentRead(ctx, in.ProjectID, in.AgentID)
	if err != nil {
		return recoveryBlockAt(result, "agent_read", err.Error())
	}
	if !agent.Enabled || agent.Role != model.AgentRoleCoding {
		return recoveryBlockAt(result, "agent_read", "exact coding Agent is disabled or has the wrong role")
	}
	agents := []model.Agent{agent}
	binding, ok := s.resolveLocalAgentBinding(in.ProjectID, agent, agents)
	if !ok || binding.Validate() != nil {
		return recoveryBlockAt(result, "binding_resolve", "configured coding Agent session is unavailable")
	}
	if binding.SessionKey != attempt.AirelaySessionKey {
		return recoveryBlockAt(result, "binding_resolve", "configured session does not match the durable Attempt")
	}
	status, statusErr := s.Airelay.Status(ctx, attempt.AirelaySessionKey)
	result.SessionState, result.ControllerReachable = status.State, status.ControllerReachable
	if statusErr != nil || !status.ControllerReachable {
		if statusErr != nil {
			result.Reason = statusErr.Error()
		} else {
			result.Reason = "Airelay controller is unreachable"
		}
		result.Phase, result.RecoveryEvent = "session_probe", "recovery_blocked_controller_unreachable"
		return result, nil
	}
	if status.State == "running" || status.State == "waiting" {
		result.Outcome, result.Recoverable, result.Phase, result.RecoveryEvent = "already_active", true, "session_probe", "recovery_noop_active_turn"
		return result, nil
	}
	if status.State != "idle" {
		return recoveryBlockAt(result, "session_probe", "Airelay session is not safely recoverable")
	}
	if runtime.AgentID != attempt.AgentID || runtime.SessionKey != attempt.AirelaySessionKey || runtime.RestartRequired {
		runtime.AgentID = attempt.AgentID
		runtime.SessionKey = attempt.AirelaySessionKey
		runtime.RestartRequired = false
		if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
			return recoveryBlockAt(result, "runtime_rebind", err.Error())
		}
		if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, in.ProjectID, in.TrainID), runtime, 0o600); err != nil {
			return recoveryBlockAt(result, "runtime_rebind", err.Error())
		}
	}
	if err := trainv2.DispatchAttempt(ctx, trainv2.StartDependencies{Hub: s.Hub, Airelay: s.Airelay, StateDir: s.Config.StateDir, MaterializePacket: s.materializeTrainV2Packet}, train, item, attempt, runtime, ""); err != nil {
		return recoveryBlockAt(result, "dispatch", err.Error())
	}
	result.Outcome, result.Recoverable, result.Phase, result.RecoveryEvent = "recovered", true, "dispatch", "recovery_packet_delivered"
	return result, nil
}

func recoveryBlock(result AgentRecoveryResult, reason string) (AgentRecoveryResult, error) {
	return recoveryBlockAt(result, result.Phase, reason)
}

func recoveryBlockAt(result AgentRecoveryResult, phase, reason string) (AgentRecoveryResult, error) {
	result.Phase, result.Reason, result.RecoveryEvent = phase, reason, "recovery_blocked"
	return result, fmt.Errorf("Agent recovery blocked at %s: %s", phase, reason)
}
