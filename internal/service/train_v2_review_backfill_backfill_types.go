package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

type TrainV2ReviewBackfillItem struct {
	Position      int    `json:"position"`
	TaskID        string `json:"task_id"`
	AttemptNumber uint64 `json:"attempt_number"`
	ReportPath    string `json:"report_path"`
	ReportSHA256  string `json:"report_sha256"`
	ReviewPath    string `json:"review_path"`
	ReviewedHead  string `json:"reviewed_head"`
}
type TrainV2ReviewBackfillResult struct {
	DryRun          bool                        `json:"dry_run"`
	Applied         bool                        `json:"applied"`
	AlreadyMigrated bool                        `json:"already_migrated"`
	ProjectID       string                      `json:"project_id"`
	TrainID         string                      `json:"train_id"`
	ItemStart       int                         `json:"item_start"`
	ItemEnd         int                         `json:"item_end"`
	HubBefore       string                      `json:"hub_before"`
	HubAfter        string                      `json:"hub_after,omitempty"`
	ReceiptPath     string                      `json:"receipt_path"`
	ChangedPaths    []string                    `json:"changed_paths,omitempty"`
	Items           []TrainV2ReviewBackfillItem `json:"items"`
}
type TrainV2ReviewBackfillReceipt struct {
	OperationID string                       `json:"operation_id"`
	Status      string                       `json:"status"`
	Result      *TrainV2ReviewBackfillResult `json:"result,omitempty"`
	Error       string                       `json:"error,omitempty"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
}
type trainV2ReviewBackfillHubReceipt struct {
	SchemaVersion int                         `json:"schema_version"`
	ProjectID     string                      `json:"project_id"`
	TrainID       string                      `json:"train_id"`
	ItemStart     int                         `json:"item_start"`
	ItemEnd       int                         `json:"item_end"`
	State         string                      `json:"state"`
	HubBefore     string                      `json:"hub_before"`
	HubAfter      string                      `json:"hub_after,omitempty"`
	Items         []TrainV2ReviewBackfillItem `json:"items"`
	Reason        string                      `json:"reason"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}
type trainV2ReviewBackfillIdentity struct {
	ProjectID   string `json:"project_id"`
	TrainID     string `json:"train_id"`
	ItemStart   int    `json:"item_start"`
	ItemEnd     int    `json:"item_end"`
	Apply       bool   `json:"apply"`
	HubRevision string `json:"hub_revision,omitempty"`
}

func trainV2ReviewBackfillPath(projectID, trainID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-review-backfill/" + trainID + ".json"
}
func trainV2ReviewBackfillReceipt(operation durableMutationOperation) TrainV2ReviewBackfillReceipt {
	receipt := TrainV2ReviewBackfillReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status == "completed" && len(operation.Result) > 0 {
		var result TrainV2ReviewBackfillResult
		if err := json.Unmarshal(operation.Result, &result); err != nil {
			receipt.Status = "failed"
			receipt.Error = "invalid durable Train review backfill result"
		} else {
			receipt.Result = &result
		}
	}
	return receipt
}
func (s *Service) TrainV2ReviewBackfillAsync(ctx context.Context, in TrainV2ReviewBackfillInput) (TrainV2ReviewBackfillReceipt, error) {
	if err := validateTrainV2ReviewBackfillInput(in); err != nil {
		return TrainV2ReviewBackfillReceipt{}, err
	}
	operation, err := s.enqueueTypedDurableMutationWithIdentity(ctx, "train-v2-review-backfill", in.ProjectID, in, trainV2ReviewBackfillIdentity{
		ProjectID:   in.ProjectID,
		TrainID:     in.TrainID,
		ItemStart:   in.ItemStart,
		ItemEnd:     in.ItemEnd,
		Apply:       in.Apply,
		HubRevision: s.sharedBootstrapMarkerRevision(ctx, in.ProjectID),
	})
	if err != nil {
		return TrainV2ReviewBackfillReceipt{}, err
	}
	return trainV2ReviewBackfillReceipt(operation), nil
}
func (s *Service) TrainV2ReviewBackfillOperationStatus(ctx context.Context, operationID string) (TrainV2ReviewBackfillReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return TrainV2ReviewBackfillReceipt{}, err
	}
	if operation.Kind != "train-v2-review-backfill" {
		return TrainV2ReviewBackfillReceipt{}, fmt.Errorf("operation is not a Train review backfill")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return TrainV2ReviewBackfillReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return trainV2ReviewBackfillReceipt(operation), nil
}
