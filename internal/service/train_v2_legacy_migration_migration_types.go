package service

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const (
	TrainV2LegacyActionMarkHistorical   = "mark_historical"
	TrainV2LegacyActionRecoverIntegrate = "recover_integration"
)

type TrainV2LegacyStateMigrationAction struct {
	Action                    string `json:"action"`
	TrainID                   string `json:"train_id"`
	TrainSHA256               string `json:"train_sha256"`
	IntegrationSHA256         string `json:"integration_sha256,omitempty"`
	IntegrationMutationID     string `json:"integration_mutation_id,omitempty"`
	IntegrationMutationSHA256 string `json:"integration_mutation_sha256,omitempty"`
}
type TrainV2LegacyStateMigrationInput struct {
	ProjectID           string                              `json:"project_id"`
	Actions             []TrainV2LegacyStateMigrationAction `json:"actions"`
	Apply               bool                                `json:"apply"`
	ExpectedHubRevision string                              `json:"expected_hub_revision,omitempty"`
	Reason              string                              `json:"reason"`
}
type TrainV2LegacyStateMigrationResult struct {
	DryRun      bool                                      `json:"dry_run"`
	Applied     bool                                      `json:"applied"`
	AlreadyDone bool                                      `json:"already_done"`
	ProjectID   string                                    `json:"project_id"`
	HubBefore   string                                    `json:"hub_before"`
	HubAfter    string                                    `json:"hub_after"`
	ReceiptPath string                                    `json:"receipt_path"`
	Records     []model.TrainV2LegacyStateMigrationRecord `json:"records"`
}
type trainV2LegacyMigrationPlan struct {
	action       TrainV2LegacyStateMigrationAction
	trainPath    string
	trainRaw     []byte
	train        model.TrainV2
	opPath       string
	opRaw        []byte
	op           trainv2.IntegrationOperation
	mutationPath string
	mutationRaw  []byte
	mutation     durableMutationOperation
	record       model.TrainV2LegacyStateMigrationRecord
}
