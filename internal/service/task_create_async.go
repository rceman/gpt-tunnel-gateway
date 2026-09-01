package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const taskCreateOperationSchemaVersion = 1

const taskCreateWorkerCount = 4

type TaskCreateOperation struct {
	SchemaVersion  int                      `json:"schema_version"`
	OperationID    string                   `json:"operation_id"`
	RequestSHA256  string                   `json:"request_sha256"`
	Input          TaskAuthoringCreateInput `json:"input"`
	Status         string                   `json:"status"`
	Task           *model.TaskAuthoring     `json:"task,omitempty"`
	Operation      OperationResult          `json:"operation,omitempty"`
	Error          string                   `json:"error,omitempty"`
	RecoveryReason string                   `json:"recovery_reason,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
}

type TaskCreateReceipt struct {
	OperationID string               `json:"operation_id"`
	Status      string               `json:"status"`
	Task        *model.TaskAuthoring `json:"task,omitempty"`
	Operation   OperationResult      `json:"operation,omitempty"`
	Error       string               `json:"error,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func (o TaskCreateOperation) Receipt() TaskCreateReceipt {
	return TaskCreateReceipt{
		OperationID: o.OperationID,
		Status:      o.Status,
		Task:        o.Task,
		Operation:   o.Operation,
		Error:       o.Error,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
	}
}

func taskCreateOperationPath(stateDir, operationID string) string {
	return filepath.Join(stateDir, "operations", "task-create", operationID+".json")
}

func taskCreateRequestDigest(in TaskAuthoringCreateInput) (string, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeTaskCreateInput(in TaskAuthoringCreateInput) (TaskAuthoringCreateInput, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskAuthoringCreateInput{}, err
	}
	if in.CreatedBy == "" || strings.ContainsAny(in.CreatedBy, "\x00\r\n") {
		return TaskAuthoringCreateInput{}, fmt.Errorf("created_by is required")
	}
	typ, err := model.NormalizeTaskType(in.Type)
	if err != nil {
		return TaskAuthoringCreateInput{}, err
	}
	in.Type = typ
	if in.ADRRelation == "" {
		in.ADRRelation = model.TaskADRNoRequired
	}
	draft := trainv2.AuthoringDraft{
		Type:  in.Type,
		Title: in.Title, Objective: in.Objective, AcceptanceCriteria: in.AcceptanceCriteria,
		Constraints: in.Constraints, Priority: in.Priority, Dependencies: in.Dependencies,
		PreparationReferences: in.PreparationReferences, Metadata: in.Metadata,
		ADRRelation: in.ADRRelation, ADRReferences: in.ADRReferences,
	}
	if err := trainv2.ValidateDraft(draft); err != nil {
		return TaskAuthoringCreateInput{}, err
	}
	return in, nil
}

func (s *Service) TaskAuthoringCreateAsync(ctx context.Context, in TaskAuthoringCreateInput) (TaskCreateOperation, error) {
	in, err := normalizeTaskCreateInput(in)
	if err != nil {
		return TaskCreateOperation{}, err
	}
	requestSHA, err := taskCreateRequestDigest(in)
	if err != nil {
		return TaskCreateOperation{}, err
	}
	operationID := "task-create-" + requestSHA
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return TaskCreateOperation{}, err
	}
	metadata := make(map[string]string, len(in.Metadata)+1)
	for key, value := range in.Metadata {
		metadata[key] = value
	}
	metadata["gateway_operation_id"] = operationID
	in.Metadata = metadata
	path := taskCreateOperationPath(s.Config.StateDir, operationID)

	s.taskCreateMu.Lock()
	defer s.taskCreateMu.Unlock()
	var operation TaskCreateOperation
	if err := fsutil.ReadJSONBounded(path, 1<<20, &operation); err == nil {
		if operation.RequestSHA256 != requestSHA || operation.OperationID != operationID {
			return TaskCreateOperation{}, fmt.Errorf("task/create operation identity mismatch")
		}
		if operation.Status == "failed" || operation.Status == "outcome_unknown" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := fsutil.WriteJSONAtomic(path, operation, 0o600); err != nil {
				return TaskCreateOperation{}, err
			}
		}
		s.startTaskCreateWorker()
		s.enqueueTaskCreate(operationID)
		return operation, nil
	} else if !os.IsNotExist(err) {
		return TaskCreateOperation{}, err
	}

	now := time.Now().UTC()
	operation = TaskCreateOperation{
		SchemaVersion: taskCreateOperationSchemaVersion,
		OperationID:   operationID,
		RequestSHA256: requestSHA,
		Input:         in,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := fsutil.WriteJSONAtomic(path, operation, 0o600); err != nil {
		return TaskCreateOperation{}, err
	}
	s.startTaskCreateWorker()
	s.enqueueTaskCreate(operationID)
	return operation, nil
}

func (s *Service) TaskCreateOperationRead(ctx context.Context, operationID string) (TaskCreateOperation, error) {
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return TaskCreateOperation{}, err
	}
	var operation TaskCreateOperation
	if err := fsutil.ReadJSONBounded(taskCreateOperationPath(s.Config.StateDir, operationID), 1<<20, &operation); err != nil {
		return TaskCreateOperation{}, err
	}
	if operation.OperationID != operationID || operation.SchemaVersion != taskCreateOperationSchemaVersion {
		return TaskCreateOperation{}, fmt.Errorf("invalid task/create operation")
	}
	return operation, nil
}

func (s *Service) TaskCreateOperationStatus(ctx context.Context, operationID string) (TaskCreateReceipt, error) {
	operation, err := s.TaskCreateOperationRead(ctx, operationID)
	if err != nil {
		return TaskCreateReceipt{}, err
	}
	return operation.Receipt(), nil
}

func (s *Service) startTaskCreateWorker() {
	s.taskCreateWorkerOnce.Do(func() {
		entries, _ := os.ReadDir(filepath.Dir(taskCreateOperationPath(s.Config.StateDir, "placeholder")))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			operationID := strings.TrimSuffix(entry.Name(), ".json")
			operation, err := s.TaskCreateOperationRead(context.Background(), operationID)
			if err != nil || operation.Status == "completed" || operation.Status == "failed" || operation.Status == "outcome_unknown" {
				continue
			}
			if operation.Status == "running" {
				if err := s.recoverRunningTaskCreate(operation); err != nil {
					continue
				}
			}
			s.enqueueTaskCreate(operationID)
		}
		for i := 0; i < taskCreateWorkerCount; i++ {
			go s.taskCreateWorker()
		}
	})
}

func (s *Service) recoverRunningTaskCreate(operation TaskCreateOperation) error {
	if operation.Status != "running" {
		return nil
	}
	operation.Status = "accepted"
	operation.Error = ""
	operation.RecoveryReason = "recovered after Gateway restart; retry is idempotent"
	operation.UpdatedAt = time.Now().UTC()
	return fsutil.WriteJSONAtomic(taskCreateOperationPath(s.Config.StateDir, operation.OperationID), operation, 0o600)
}

func (s *Service) enqueueTaskCreate(operationID string) {
	select {
	case s.taskCreateWake <- operationID:
	default:
	}
}

func (s *Service) taskCreateWorker() {
	for operationID := range s.taskCreateWake {
		s.processTaskCreate(operationID)
	}
}

func (s *Service) processTaskCreate(operationID string) {
	s.taskCreateMu.Lock()
	operation, err := s.TaskCreateOperationRead(context.Background(), operationID)
	if err != nil || operation.Status == "completed" {
		s.taskCreateMu.Unlock()
		return
	}
	if _, active := s.taskCreateActive[operationID]; active {
		s.taskCreateMu.Unlock()
		return
	}
	s.taskCreateActive[operationID] = struct{}{}
	operation.Status = "running"
	operation.UpdatedAt = time.Now().UTC()
	path := taskCreateOperationPath(s.Config.StateDir, operationID)
	_ = fsutil.WriteJSONAtomic(path, operation, 0o600)
	s.taskCreateMu.Unlock()
	defer func() {
		s.taskCreateMu.Lock()
		delete(s.taskCreateActive, operationID)
		s.taskCreateMu.Unlock()
	}()

	workerCtx, cancel := s.asyncMutationContext("task/create", operation.OperationID)
	defer cancel()
	var task model.TaskAuthoring
	var result OperationResult
	if s.Durability != nil {
		task, result, err = s.taskAuthoringCreateShared(workerCtx, operation.OperationID, operation.Input)
	} else if existing, findErr := s.findTaskCreateResult(workerCtx, operation); findErr == nil {
		task = *existing
		result = OperationResult{OperationID: operationID, ProjectID: existing.ProjectID, TaskID: existing.ID, Status: existing.Status}
	} else if !errors.Is(findErr, os.ErrNotExist) {
		if asyncMutationOutcomeUnknown(findErr) {
			s.finishTaskCreateUnknown(operation, findErr)
			return
		}
		s.finishTaskCreate(operation, nil, OperationResult{OperationID: operationID, ProjectID: operation.Input.ProjectID, Status: "failed"}, findErr.Error())
		return
	} else {
		task, result, err = s.TaskAuthoringCreate(workerCtx, operation.Input)
	}
	if err != nil {
		if asyncMutationOutcomeUnknown(err) {
			s.finishTaskCreateUnknown(operation, err)
			return
		}
		s.finishTaskCreate(operation, nil, OperationResult{
			OperationID: operationID,
			ProjectID:   operation.Input.ProjectID,
			Status:      "failed",
		}, err.Error())
		return
	}
	s.finishTaskCreate(operation, &task, OperationResult{
		OperationID: operationID,
		ProjectID:   result.ProjectID,
		TaskID:      result.TaskID,
		Hub:         result.Hub,
		Status:      result.Status,
	}, "")
}

func (s *Service) finishTaskCreateUnknown(operation TaskCreateOperation, err error) {
	operation.Status = "outcome_unknown"
	operation.Operation = OperationResult{OperationID: operation.OperationID, ProjectID: operation.Input.ProjectID, Status: "outcome_unknown"}
	operation.Error = err.Error()
	operation.RecoveryReason = "bounded worker context ended before Hub outcome was proven; retry is idempotent"
	operation.UpdatedAt = time.Now().UTC()
	_ = fsutil.WriteJSONAtomic(taskCreateOperationPath(s.Config.StateDir, operation.OperationID), operation, 0o600)
}

func (s *Service) findTaskCreateResult(ctx context.Context, operation TaskCreateOperation) (*model.TaskAuthoring, error) {
	all, err := s.taskAuthoringAll(ctx, operation.Input.ProjectID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Metadata["gateway_operation_id"] == operation.OperationID {
			return &all[i], nil
		}
	}
	return nil, os.ErrNotExist
}

func (s *Service) finishTaskCreate(operation TaskCreateOperation, task *model.TaskAuthoring, result OperationResult, failure string) {
	if task != nil {
		operation.Status = "completed"
		operation.Task = task
		operation.Operation = result
	} else {
		operation.Status = "failed"
		operation.Operation = result
		operation.Error = failure
	}
	operation.UpdatedAt = time.Now().UTC()
	_ = fsutil.WriteJSONAtomic(taskCreateOperationPath(s.Config.StateDir, operation.OperationID), operation, 0o600)
}
