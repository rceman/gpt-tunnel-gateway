package service

import (
	"context"
	"encoding/json"
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
	if err != nil || operation.Status == "completed" {
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
	_ = s.writeDurableMutation(operation)
	s.durableMutationMu.Unlock()
	defer func() {
		s.durableMutationMu.Lock()
		delete(s.durableMutationActive, operationID)
		s.durableMutationMu.Unlock()
	}()

	// Durable workers run after the request context is gone. Rebind the
	// immutable originating Session so outbound Agent IPC keeps its
	// provenance instead of falling back to the Gateway identity.
	workerCtx := WithAgentSessionID(context.Background(), operation.SessionID)
	result, runErr := s.executeDurableMutation(workerCtx, operation)
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		operation.Status = "failed"
		operation.Error = runErr.Error()
		operation.Result = nil
	} else {
		operation.Status = "completed"
		operation.Error = ""
		operation.Result = result
	}
	_ = s.writeDurableMutation(operation)
}
func (s *Service) executeDurableMutation(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	switch operation.Kind {
	case "task-authoring-update", "task-authoring-ready", "train-v2-integrate", "train-v2-full-proof", "train-v2-review-backfill", "train-v2-start", "train-v2-advance", "train-v2-correction-start":
		return s.durableMutationExecutionSet1(ctx, operation)
	case "train-v2-retire", "train-v2-reconcile", "adr-create", "agent-register", "agent-prompt", "agent-recover", "agent-interrupt", "agent-update":
		return s.durableMutationExecutionSet2(ctx, operation)
	case "agent-disable", "watcher-guide-update", "watcher-nudge", "project-configuration-update", "project-remove", "task-supersede", "task-work", "task-finalize":
		return s.durableMutationExecutionSet3(ctx, operation)
	case "train-attempt-finalize", "train-v2-create", "train-v2-add", "train-v2-cutover", "train-attempt-review":
		return s.durableMutationExecutionSet4(ctx, operation)
	default:
		return nil, fmt.Errorf("unsupported durable mutation kind %q", operation.Kind)
	}
}
