package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainV2LifecycleReceipt is the bounded initiation/status view for a
// server-owned Train start or advance. The underlying Train state machine
// remains the sole authority for execution state.
type TrainV2LifecycleReceipt struct {
	OperationID string               `json:"operation_id"`
	Kind        string               `json:"kind"`
	Status      string               `json:"status"`
	Result      *trainv2.StartResult `json:"result,omitempty"`
	Error       string               `json:"error,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

// trainV2AdvanceIdentity binds the public receipt to the local execution
// generation that the worker is advancing. The runtime binding is a bounded,
// Gateway-owned snapshot and avoids a Hub refresh before the receipt is
// durably written. A changed item/attempt/session generation therefore gets a
// new operation identity instead of reusing an older receipt.
type trainV2AdvanceIdentity struct {
	ProjectID       string    `json:"project_id"`
	TrainID         string    `json:"train_id"`
	HubRevision     string    `json:"hub_revision,omitempty"`
	RuntimePresent  bool      `json:"runtime_present"`
	ItemPosition    int       `json:"item_position,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	AttemptNumber   uint64    `json:"attempt_number,omitempty"`
	AgentID         string    `json:"agent_id,omitempty"`
	SessionKey      string    `json:"session_key,omitempty"`
	WorktreePath    string    `json:"worktree_path,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	RestartRequired bool      `json:"restart_required,omitempty"`
}

type taskWorkIdentity struct {
	ProjectID   string `json:"project_id"`
	TaskID      string `json:"task_id"`
	HubRevision string `json:"hub_revision,omitempty"`
}

func (s *Service) localHubRevision(ctx context.Context) string {
	snapshot, err := s.Hub.ReadSnapshot(ctx)
	if err != nil {
		return ""
	}
	defer snapshot.Close()
	return snapshot.Revision()
}

func (s *Service) trainV2AdvanceIdentity(ctx context.Context, in TrainV2AdvanceInput) trainV2AdvanceIdentity {
	identity := trainV2AdvanceIdentity{ProjectID: in.ProjectID, TrainID: in.TrainID, HubRevision: s.localHubRevision(ctx)}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return identity
	}
	identity.RuntimePresent = true
	identity.ItemPosition = runtime.ItemPosition
	identity.TaskID = runtime.TaskID
	identity.AttemptNumber = runtime.AttemptNumber
	identity.AgentID = runtime.AgentID
	identity.SessionKey = runtime.SessionKey
	identity.WorktreePath = runtime.WorktreePath
	identity.StartedAt = runtime.StartedAt
	identity.RestartRequired = runtime.RestartRequired
	return identity
}

func trainV2LifecycleReceipt(operation durableMutationOperation) TrainV2LifecycleReceipt {
	receipt := TrainV2LifecycleReceipt{
		OperationID: operation.OperationID,
		Kind:        operation.Kind,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Result trainv2.StartResult `json:"result"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable Train lifecycle result"
		return receipt
	}
	receipt.Result = &result.Result
	return receipt
}

func (s *Service) TrainV2StartAsync(ctx context.Context, in TrainV2StartInput) (TrainV2LifecycleReceipt, error) {
	if in.StartedBy == "" {
		return TrainV2LifecycleReceipt{}, fmt.Errorf("started_by is required")
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "train-v2-start", in.ProjectID, in)
	if err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	return trainV2LifecycleReceipt(operation), nil
}

func (s *Service) TrainV2StartOperationStatus(ctx context.Context, operationID string) (TrainV2LifecycleReceipt, error) {
	return s.trainV2LifecycleOperationStatus(ctx, operationID, "train-v2-start")
}

func (s *Service) TrainV2AdvanceAsync(ctx context.Context, in TrainV2AdvanceInput) (TrainV2LifecycleReceipt, error) {
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutationWithIdentity(ctx, "train-v2-advance", in.ProjectID, in, s.trainV2AdvanceIdentity(ctx, in))
	if err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	return trainV2LifecycleReceipt(operation), nil
}

func (s *Service) TrainV2AdvanceOperationStatus(ctx context.Context, operationID string) (TrainV2LifecycleReceipt, error) {
	return s.trainV2LifecycleOperationStatus(ctx, operationID, "train-v2-advance")
}

func (s *Service) trainV2LifecycleOperationStatus(ctx context.Context, operationID, kind string) (TrainV2LifecycleReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2LifecycleReceipt{}, err
	}
	if operation.Kind != kind {
		return TrainV2LifecycleReceipt{}, fmt.Errorf("operation is not a %s lifecycle mutation", kind)
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2LifecycleReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2LifecycleReceipt(operation), nil
}
