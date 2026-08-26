package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) sharedTrainEvidence() (trainv2.EvidenceStore, error) {
	if s.TrainEvidence == nil {
		return nil, fmt.Errorf("Train evidence persistence is unavailable")
	}
	return s.TrainEvidence, nil
}

func (s *Service) attemptCompletionID(trainID, taskID string, attempt uint64) (string, error) {
	evidence, err := s.sharedTrainEvidence()
	if err != nil {
		return "", err
	}
	return evidence.AttemptCompletionID(trainID, taskID, attempt)
}

func (s *Service) commitSharedTrain(ctx context.Context, operationID string, train model.TrainV2, kind string, intents ...sqlitestore.SharedReplicaIntent) error {
	if s.Durability == nil {
		return fmt.Errorf("Shared durability is unavailable")
	}
	return trainv2.CommitSharedTrain(ctx, trainv2.StartDependencies{Shared: s.Durability, OperationID: operationID, ReplicaIntents: intents}, train, kind, train.UpdatedAt)
}
