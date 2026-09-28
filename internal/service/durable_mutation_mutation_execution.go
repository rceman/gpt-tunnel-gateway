package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Service) durableMutationWorker() {
	for operationID := range s.durableMutationWake {
		s.processDurableMutation(operationID)
	}
}
func (s *Service) processDurableMutation(operationID string) {
	s.durableMutationMu.Lock()
	operation, err := s.readDurableMutation(operationID)
	if err != nil || durableMutationTerminal(operation.Status) {
		s.durableMutationMu.Unlock()
		return
	}
	if _, active := s.durableMutationActive[operationID]; active {
		s.durableMutationMu.Unlock()
		return
	}
	s.durableMutationActive[operationID] = struct{}{}
	operation.Status = "running"
	operation.UpdatedAt = time.Now().UTC()
	if err := s.writeDurableMutation(operation); err != nil {
		delete(s.durableMutationActive, operationID)
		s.durableMutationMu.Unlock()
		return
	}
	s.durableMutationMu.Unlock()
	defer func() {
		s.durableMutationMu.Lock()
		delete(s.durableMutationActive, operationID)
		s.durableMutationMu.Unlock()
	}()

	// Durable workers run after the request context is gone. Rebind the
	// immutable originating Session so outbound Agent IPC keeps its
	// provenance instead of falling back to the Gateway identity.
	workerCtx, cancel := s.asyncMutationContext(operation.Kind, operation.OperationID)
	defer cancel()
	workerCtx = WithAgentSessionID(workerCtx, operation.SessionID)
	workerCtx = withDurableMutationOperationID(workerCtx, operation.OperationID)
	execute := s.durableMutationExecutor
	if execute == nil {
		execute = s.executeDurableMutation
	}
	result, runErr := execute(workerCtx, operation)
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	// The handler may have durably written capture evidence mid-run (e.g. the
	// integration write-ahead record). Re-read so the finish write never
	// clobbers it; on read failure leave the record running so restart
	// recovery replays with the on-disk capture intact.
	if fresh, readErr := s.readDurableMutation(operationID); readErr == nil {
		operation = fresh
	} else {
		return
	}
	operation.UpdatedAt = time.Now().UTC()
	hookAttempts := taskLifecycleHookAttempts(operation.Result)
	if runErr != nil {
		operation.Status = "failed"
		var procedureErr *procedureExecutionError
		if errors.As(runErr, &procedureErr) && procedureErr.OutcomeUnknown {
			operation.Status = "outcome_unknown"
			operation.RecoveryReason = "Procedure outcome was not proven before the bounded execution ended"
		} else if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) {
			operation.Status = "outcome_unknown"
			operation.RecoveryReason = "bounded worker context ended before the Operation outcome was proven"
		}
		operation.Error = runErr.Error()
		operation.Result = mergeTaskLifecycleHookAttempts(nil, hookAttempts)
	} else {
		operation.Status = "completed"
		operation.Error = ""
		operation.Result = mergeTaskLifecycleHookAttempts(result, hookAttempts)
	}
	if err := s.writeDurableMutation(operation); err == nil && operation.Kind == procedureExecutionKind {
		_ = s.recordAgentWorkFinishedOperationOutcome(operation)
	}
}
func (s *Service) executeDurableMutation(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	if operation.Kind == procedureExecutionKind {
		return s.executeProcedureOperation(ctx, operation)
	}
	switch operation.Kind {
	case taskExecutionSubmitKind, "task-execution-integrate", "task-execution-test", "task-authoring-update", "task-authoring-ready":
		return s.durableMutationExecutionSet1(ctx, operation)
	case "adr-create", "agent-prompt", "agent-interrupt", "agent-update":
		return s.durableMutationExecutionSet2(ctx, operation)
	case "agent-disable", "project-configuration-update", "task-supersede":
		return s.durableMutationExecutionSet3(ctx, operation)
	default:
		return nil, fmt.Errorf("unsupported durable mutation kind %q", operation.Kind)
	}
}
