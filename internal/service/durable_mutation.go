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

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const durableMutationSchemaVersion = 1

type durableMutationOperation struct {
	SchemaVersion int             `json:"schema_version"`
	OperationID   string          `json:"operation_id"`
	Kind          string          `json:"kind"`
	RequestSHA256 string          `json:"request_sha256"`
	SessionID     string          `json:"session_id,omitempty"`
	ProjectID     string          `json:"project_id"`
	Input         json.RawMessage `json:"input"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func durableMutationPath(stateDir, operationID string) string {
	return filepath.Join(stateDir, "operations", "mutations", operationID+".json")
}

func durableMutationDigest(kind, sessionID string, input []byte) string {
	return durableMutationDigestWithIdentity(kind, sessionID, input, nil)
}

func durableMutationDigestWithIdentity(kind, sessionID string, input, identity []byte) string {
	hash := sha256.New()
	hash.Write([]byte(kind))
	hash.Write([]byte{0})
	hash.Write([]byte(sessionID))
	hash.Write([]byte{0})
	hash.Write(input)
	if identity != nil {
		hash.Write([]byte{0})
		hash.Write(identity)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *Service) startDurableMutationWorker() {
	s.durableMutationWorkerOnce.Do(func() {
		dir := filepath.Dir(durableMutationPath(s.Config.StateDir, "placeholder"))
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				s.enqueueDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
			}
		}
		for i := 0; i < 4; i++ {
			go s.durableMutationWorker()
		}
	})
}

func (s *Service) enqueueDurableMutation(operationID string) {
	select {
	case s.durableMutationWake <- operationID:
	default:
	}
}

func (s *Service) enqueueTypedDurableMutation(ctx context.Context, kind, projectID string, input any) (durableMutationOperation, error) {
	return s.enqueueTypedDurableMutationWithIdentity(ctx, kind, projectID, input, nil)
}

func (s *Service) enqueueTypedDurableMutationWithIdentity(ctx context.Context, kind, projectID string, input, identity any) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return durableMutationOperation{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return durableMutationOperation{}, err
	}
	sessionID := AgentSessionID(ctx)
	var identityRaw []byte
	if identity != nil {
		identityRaw, err = json.Marshal(identity)
		if err != nil {
			return durableMutationOperation{}, err
		}
	}
	digest := durableMutationDigestWithIdentity(kind, sessionID, raw, identityRaw)
	operationID := "mutation-" + digest
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation, err := s.readDurableMutation(operationID)
	if err == nil {
		if operation.RequestSHA256 != digest || operation.Kind != kind {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(err) {
		return durableMutationOperation{}, err
	}
	now := time.Now().UTC()
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          kind,
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     projectID,
		Input:         raw,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}

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
	case "task-authoring-update":
		var input TaskAuthoringUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		// The operation marker makes a retry after a process crash safe: if the
		// Hub write committed before the receipt did, the durable Task itself
		// proves that this exact operation already applied.
		if current, err := s.TaskAuthoringRead(ctx, input.ProjectID, input.TaskID); err == nil && current.Metadata != nil && current.Metadata["gateway_operation_id"] == operation.OperationID {
			return json.Marshal(map[string]any{
				"task": current,
				"operation": OperationResult{
					OperationID: operation.OperationID,
					ProjectID:   current.ProjectID,
					TaskID:      current.ID,
					Status:      current.Status,
				},
			})
		}
		task, result, err := s.TaskAuthoringUpdate(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(map[string]any{"task": task, "operation": result})
	case "task-authoring-ready":
		var input TaskAuthoringReadyInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		if current, err := s.TaskAuthoringRead(ctx, input.ProjectID, input.TaskID); err == nil && current.Status == model.TaskAuthoringReady && current.ReadySeal != nil && current.ReadySeal.Revision == input.ExpectedRevision && current.ReadySeal.RevisionSHA256 == input.ExpectedRevisionSHA256 && current.ReadySeal.ReadyBy == input.ReadyBy {
			return json.Marshal(map[string]any{
				"task": current,
				"operation": OperationResult{
					OperationID: operation.OperationID,
					ProjectID:   current.ProjectID,
					TaskID:      current.ID,
					Status:      current.Status,
				},
			})
		}
		task, result, err := s.TaskAuthoringReady(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(map[string]any{"task": task, "operation": result})
	case "train-v2-integrate":
		var input TrainV2IntegrateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		receipt, result, err := s.TrainV2Integrate(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(map[string]any{"receipt": receipt, "operation": result})
	case "train-v2-start":
		var input TrainV2StartInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2Start(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": result})
	case "train-v2-advance":
		var input TrainV2AdvanceInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2Advance(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": result})
	case "adr-create":
		var input ADRCreateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.ADRCreate(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(result)
	case "agent-register":
		var input AgentRegisterInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		agent, result, err := s.AgentRegister(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"agent": agent, "operation": result})
	case "agent-prompt":
		var input AgentPromptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentPrompt(authority.WithPlannerOrDelivery(ctx), input.ProjectID, input.Message)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-recover":
		var input AgentRecoverInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentRecover(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-interrupt":
		var input AgentInterruptInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.AgentInterrupt(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "agent-update":
		var input AgentUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		agent, result, err := s.AgentUpdate(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"agent": agent, "operation": result})
	case "agent-disable":
		var input AgentDisableInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		agent, result, err := s.AgentDisable(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"agent": agent, "operation": result})
	case "watcher-guide-update":
		var input WatcherGuideUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.WatcherGuideUpdate(ctx, input)
		if err != nil {
			return nil, err
		}
		guide, err := s.WatcherGuideRead(ctx, input.ProjectID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"guide": guide, "operation": result})
	case "watcher-nudge":
		var input WatcherNudgeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.WatcherNudge(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "project-configuration-update":
		var input ProjectConfigurationUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		configuration, result, err := s.ProjectConfigurationUpdate(projectConfigurationMutationContext(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"configuration": configuration, "operation": result})
	case "project-remove":
		var input ProjectRemoveInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.ProjectRemove(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "task-supersede":
		var input TaskSupersedeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		task, result, err := s.TaskSupersede(ctx, input.OldTaskID, input.Task)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"task": task, "operation": result})
	case "task-work":
		var input TaskWorkInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TaskWork(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "task-finalize":
		var input TaskFinalizeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TaskFinalize(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "train-attempt-finalize":
		var input TrainV2AttemptFinalizeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2AttemptFinalize(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "train-v2-create":
		var input TrainV2CreateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		train, result, err := s.TrainV2Create(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"train": train, "operation": result})
	case "train-v2-add":
		var input TrainV2AddInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		train, result, err := s.TrainV2Add(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"train": train, "operation": result})
	case "train-v2-cutover":
		var input TrainV2CutoverInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		receipt, result, err := s.TrainV2Cutover(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"receipt": receipt, "operation": result})
	case "train-attempt-review":
		var input TrainV2AttemptReviewInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2AttemptReview(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	default:
		return nil, fmt.Errorf("unsupported durable mutation kind %q", operation.Kind)
	}
}

func (s *Service) enqueueTrainV2Integrate(ctx context.Context, in TrainV2IntegrateInput) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return durableMutationOperation{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return durableMutationOperation{}, err
	}
	input, err := json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	sessionID := AgentSessionID(ctx)
	digest := durableMutationDigest("train-v2-integrate", sessionID, input)
	operationID := "mutation-" + digest
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation, err := s.readDurableMutation(operationID)
	if err == nil {
		if operation.RequestSHA256 != digest || operation.Kind != "train-v2-integrate" {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(err) {
		return durableMutationOperation{}, err
	}
	now := time.Now().UTC()
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          "train-v2-integrate",
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     in.ProjectID,
		Input:         input,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}

func (s *Service) enqueueTaskAuthoringReady(ctx context.Context, in TaskAuthoringReadyInput) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return durableMutationOperation{}, err
	}
	if err := model.ValidateCanonicalTaskID(in.TaskID); err != nil {
		return durableMutationOperation{}, err
	}
	if in.ExpectedRevision < 1 || strings.TrimSpace(in.ReadyBy) == "" || strings.ContainsAny(in.ReadyBy, "\x00\r\n") {
		return durableMutationOperation{}, fmt.Errorf("expected_revision and ready_by are required")
	}
	input, err := json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	sessionID := AgentSessionID(ctx)
	digest := durableMutationDigest("task-authoring-ready", sessionID, input)
	operationID := "mutation-" + digest
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation, err := s.readDurableMutation(operationID)
	if err == nil {
		if operation.RequestSHA256 != digest || operation.Kind != "task-authoring-ready" {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(err) {
		return durableMutationOperation{}, err
	}
	now := time.Now().UTC()
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          "task-authoring-ready",
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     in.ProjectID,
		Input:         input,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}

func (s *Service) readDurableMutation(operationID string) (durableMutationOperation, error) {
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	var operation durableMutationOperation
	if err := fsutil.ReadJSONBounded(durableMutationPath(s.Config.StateDir, operationID), 1<<20, &operation); err != nil {
		return durableMutationOperation{}, err
	}
	if operation.SchemaVersion != durableMutationSchemaVersion || operation.OperationID != operationID || operation.Kind == "" {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation operation")
	}
	if len(operation.RequestSHA256) != sha256.Size*2 {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation request digest")
	}
	if _, err := hex.DecodeString(operation.RequestSHA256); err != nil {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation request digest: %w", err)
	}
	switch operation.Status {
	case "accepted", "running", "completed", "failed":
	default:
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation status %q", operation.Status)
	}
	if len(operation.Input) == 0 || operation.ProjectID == "" {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation payload")
	}
	return operation, nil
}

func (s *Service) writeDurableMutation(operation durableMutationOperation) error {
	return fsutil.WriteJSONAtomic(durableMutationPath(s.Config.StateDir, operation.OperationID), operation, 0o600)
}

func (s *Service) enqueueTaskAuthoringUpdate(ctx context.Context, in TaskAuthoringUpdateInput) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return durableMutationOperation{}, err
	}
	if err := model.ValidateCanonicalTaskID(in.TaskID); err != nil {
		return durableMutationOperation{}, err
	}
	if in.ExpectedRevision < 1 || strings.TrimSpace(in.UpdatedBy) == "" || strings.ContainsAny(in.UpdatedBy, "\x00\r\n") {
		return durableMutationOperation{}, fmt.Errorf("expected_revision and updated_by are required")
	}
	input, err := json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	sessionID := AgentSessionID(ctx)
	digest := durableMutationDigest("task-authoring-update", sessionID, input)
	operationID := "mutation-" + digest
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	if in.Metadata != nil && (*in.Metadata)["gateway_operation_id"] != "" {
		return durableMutationOperation{}, fmt.Errorf("metadata gateway_operation_id is server-owned")
	}
	metadata := make(map[string]string, lenValue(in.Metadata)+1)
	if in.Metadata != nil {
		for key, value := range *in.Metadata {
			metadata[key] = value
		}
	}
	metadata["gateway_operation_id"] = operationID
	in.Metadata = &metadata
	input, err = json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation, err := s.readDurableMutation(operationID)
	if err == nil {
		if operation.RequestSHA256 != digest || operation.Kind != "task-authoring-update" {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(err) {
		return durableMutationOperation{}, err
	}
	now := time.Now().UTC()
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          "task-authoring-update",
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     in.ProjectID,
		Input:         input,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}

func lenValue(values *map[string]string) int {
	if values == nil {
		return 0
	}
	return len(*values)
}
