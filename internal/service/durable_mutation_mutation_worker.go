package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) startDurableMutationWorker() {
	s.durableMutationWorkerOnce.Do(func() {
		dir := filepath.Dir(durableMutationPath(s.Config.StateDir, "placeholder"))
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				operationID := strings.TrimSuffix(entry.Name(), ".json")
				operation, err := s.readDurableMutation(operationID)
				if err != nil || operation.Status == "completed" || operation.Status == "failed" {
					continue
				}
				if operation.Status == "running" {
					if err := s.recoverRunningDurableMutation(operation); err != nil {
						continue
					}
				}
				s.enqueueDurableMutation(operationID)
			}
		}
		for i := 0; i < 4; i++ {
			go s.durableMutationWorker()
		}
	})
}
func (s *Service) recoverRunningDurableMutation(operation durableMutationOperation) error {
	if operation.Status != "running" {
		return nil
	}
	operation.Status = "accepted"
	operation.Error = ""
	operation.RecoveryReason = "recovered after Gateway restart; retry is idempotent"
	operation.UpdatedAt = time.Now().UTC()
	return s.writeDurableMutation(operation)
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
