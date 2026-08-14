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

type TaskCreateOperation struct {
	SchemaVersion int                      `json:"schema_version"`
	OperationID   string                   `json:"operation_id"`
	RequestSHA256 string                   `json:"request_sha256"`
	Input         TaskAuthoringCreateInput `json:"input"`
	Status        string                   `json:"status"`
	Task          *model.TaskAuthoring     `json:"task,omitempty"`
	Operation     OperationResult          `json:"operation,omitempty"`
	Error         string                   `json:"error,omitempty"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
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
	return TaskCreateReceipt{OperationID: o.OperationID, Status: o.Status, Task: o.Task, Operation: o.Operation, Error: o.Error, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt}
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
	if in.ADRRelation == "" {
		in.ADRRelation = model.TaskADRNoRequired
	}
	draft := trainv2.AuthoringDraft{
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
		if operation.Status == "failed" {
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
		go s.taskCreateWorker()
	})
}

func (s *Service) enqueueTaskCreate(operationID string) {
	select {
	case s.taskCreateWake <- operationID:
	default:
	}
}

func (s *Service) taskCreateWorker() {
	entries, _ := os.ReadDir(filepath.Dir(taskCreateOperationPath(s.Config.StateDir, "placeholder")))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		s.enqueueTaskCreate(strings.TrimSuffix(entry.Name(), ".json"))
	}
	for operationID := range s.taskCreateWake {
		s.processTaskCreate(operationID)
	}
}

func (s *Service) processTaskCreate(operationID string) {
	operation, err := s.TaskCreateOperationRead(context.Background(), operationID)
	if err != nil || operation.Status == "completed" {
		return
	}
	s.taskCreateMu.Lock()
	operation.Status = "running"
	operation.UpdatedAt = time.Now().UTC()
	path := taskCreateOperationPath(s.Config.StateDir, operationID)
	_ = fsutil.WriteJSONAtomic(path, operation, 0o600)
	s.taskCreateMu.Unlock()

	if existing, findErr := s.findTaskCreateResult(context.Background(), operation); findErr == nil {
		s.finishTaskCreate(operation, existing, OperationResult{OperationID: operationID, ProjectID: existing.ProjectID, TaskID: existing.ID, Status: existing.Status}, "")
		return
	} else if !errors.Is(findErr, os.ErrNotExist) {
		s.finishTaskCreate(operation, nil, OperationResult{OperationID: operationID, ProjectID: operation.Input.ProjectID, Status: "failed"}, findErr.Error())
		return
	}
	task, result, err := s.TaskAuthoringCreate(context.Background(), operation.Input)
	if err != nil {
		s.finishTaskCreate(operation, nil, OperationResult{OperationID: operationID, ProjectID: operation.Input.ProjectID, Status: "failed"}, err.Error())
		return
	}
	s.finishTaskCreate(operation, &task, OperationResult{OperationID: operationID, ProjectID: result.ProjectID, TaskID: result.TaskID, Hub: result.Hub, Status: result.Status}, "")
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
