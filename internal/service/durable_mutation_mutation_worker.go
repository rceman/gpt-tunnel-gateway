package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) startDurableMutationWorker() {
	s.durableMutationWorkerOnce.Do(func() {
		dir := filepath.Dir(durableMutationPath(s.Config.StateDir, "placeholder"))
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				operationID := strings.TrimSuffix(entry.Name(), ".json")
				operation, err := s.readDurableMutation(operationID)
				if err != nil {
					continue
				}
				if model.ValidateOperationID(operationID) != nil && strings.HasPrefix(operationID, "mutation-") {
					adopted, adoptedOK, adoptErr := s.adoptLegacyDurableMutationOnStartup(context.Background(), operation)
					if adoptErr != nil {
						continue
					}
					if adoptedOK {
						operationID = adopted.OperationID
						operation = adopted
					}
				}
				if !replayDurableMutationOnStartup(operation.Status) {
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

func replayDurableMutationOnStartup(status string) bool {
	return status == "accepted" || status == "running"
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

func (s *Service) adoptLegacyDurableMutationForRequest(ctx context.Context, operationID, projectID, kind, digest string) (durableMutationOperation, bool, error) {
	legacy, err := s.readDurableMutation("mutation-" + digest)
	if err != nil {
		if os.IsNotExist(err) {
			return durableMutationOperation{}, false, nil
		}
		return durableMutationOperation{}, false, err
	}
	if legacy.RequestSHA256 != digest || legacy.ProjectID != projectID || legacy.Kind != kind {
		return durableMutationOperation{}, false, fmt.Errorf("legacy durable mutation identity mismatch")
	}
	legacyID := legacy.OperationID
	legacy.Input, err = canonicalizeAdoptedOperationJSON(legacy.Input, legacyID, operationID)
	if err != nil {
		return durableMutationOperation{}, false, fmt.Errorf("canonicalize legacy durable mutation input: %w", err)
	}
	legacy.Result, err = canonicalizeAdoptedOperationJSON(legacy.Result, legacyID, operationID)
	if err != nil {
		return durableMutationOperation{}, false, fmt.Errorf("canonicalize legacy durable mutation result: %w", err)
	}
	legacy.OperationID = operationID
	legacy.MutationID = digest
	if err := s.writeDurableMutation(legacy); err != nil {
		return durableMutationOperation{}, false, err
	}
	return legacy, true, nil
}

func (s *Service) adoptLegacyDurableMutationOnStartup(ctx context.Context, legacy durableMutationOperation) (durableMutationOperation, bool, error) {
	if s.Durability == nil {
		return durableMutationOperation{}, false, nil
	}
	projectCode, err := s.localOperationProjectCode(ctx, legacy.ProjectID)
	if err != nil {
		return durableMutationOperation{}, false, err
	}
	allocated, err := s.Durability.AllocateLocalOperation(ctx, legacy.ProjectID, projectCode, legacy.RequestSHA256, legacy.Kind, time.Now().UTC())
	if err != nil {
		return durableMutationOperation{}, false, err
	}
	if operation, readErr := s.readDurableMutation(allocated.OperationID); readErr == nil {
		if operation.RequestSHA256 != legacy.RequestSHA256 || operation.Kind != legacy.Kind || operation.ProjectID != legacy.ProjectID {
			return durableMutationOperation{}, false, fmt.Errorf("adopted durable mutation identity mismatch")
		}
		return operation, true, nil
	} else if !os.IsNotExist(readErr) {
		return durableMutationOperation{}, false, readErr
	}
	legacyID := legacy.OperationID
	legacy.Input, err = canonicalizeAdoptedOperationJSON(legacy.Input, legacyID, allocated.OperationID)
	if err != nil {
		return durableMutationOperation{}, false, fmt.Errorf("canonicalize legacy durable mutation input: %w", err)
	}
	legacy.Result, err = canonicalizeAdoptedOperationJSON(legacy.Result, legacyID, allocated.OperationID)
	if err != nil {
		return durableMutationOperation{}, false, fmt.Errorf("canonicalize legacy durable mutation result: %w", err)
	}
	legacy.OperationID = allocated.OperationID
	legacy.MutationID = allocated.MutationID
	if err := s.writeDurableMutation(legacy); err != nil {
		return durableMutationOperation{}, false, err
	}
	return legacy, true, nil
}

func (s *Service) enqueueTypedDurableMutation(ctx context.Context, kind, projectID string, input any) (durableMutationOperation, error) {
	return s.enqueueTypedDurableMutationWithIdentity(ctx, kind, projectID, input, nil)
}

func (s *Service) enqueueRepeatableAgentMutation(ctx context.Context, kind, projectID string, input any) (durableMutationOperation, error) {
	return s.enqueueTypedDurableMutationWithPolicy(ctx, kind, projectID, input, nil, true)
}

func (s *Service) enqueueTypedDurableMutationWithIdentity(ctx context.Context, kind, projectID string, input, identity any) (durableMutationOperation, error) {
	return s.enqueueTypedDurableMutationWithPolicy(ctx, kind, projectID, input, identity, false)
}

func (s *Service) enqueueTypedDurableMutationWithPolicy(ctx context.Context, kind, projectID string, input, identity any, freshAfterTerminal bool) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return durableMutationOperation{}, err
	}
	if s.Durability == nil {
		return durableMutationOperation{}, fmt.Errorf("local durability is unavailable")
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
	projectCode, err := s.localOperationProjectCode(ctx, projectID)
	if err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	now := time.Now().UTC()
	admissionInputSHA256 := durableMutationInputSHA256(raw)
	allocate := func(mutationID string) (string, error) {
		var allocated sqlitestore.LocalOperation
		var allocateErr error
		if freshAfterTerminal {
			allocated, allocateErr = s.Durability.AllocateLocalOperationWithAdmissionCoordinate(ctx, projectID, projectCode, mutationID, kind, sessionID, admissionInputSHA256, now)
		} else {
			allocated, allocateErr = s.Durability.AllocateLocalOperation(ctx, projectID, projectCode, mutationID, kind, now)
		}
		if allocateErr != nil {
			return "", allocateErr
		}
		return allocated.OperationID, nil
	}
	operationID, err := allocate(digest)
	if err != nil {
		return durableMutationOperation{}, err
	}
	operation, readErr := s.readDurableMutation(operationID)
	if readErr == nil {
		if operation.RequestSHA256 != digest || operation.Kind != kind {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if freshAfterTerminal && durableMutationTerminal(operation.Status) {
			latest, found, findErr := s.findLatestEquivalentDurableMutation(ctx, kind, projectID, projectCode, sessionID, raw)
			if findErr != nil {
				return durableMutationOperation{}, findErr
			}
			if found {
				operation = latest
				digest = operation.RequestSHA256
				operationID = operation.OperationID
			}
			if !found || durableMutationTerminal(operation.Status) {
				digest, err = freshDurableMutationDigest(kind, sessionID, raw)
				if err != nil {
					return durableMutationOperation{}, err
				}
				operationID, err = allocate(digest)
				if err != nil {
					return durableMutationOperation{}, err
				}
				operation, readErr = s.readDurableMutation(operationID)
			}
		}
	}
	if readErr == nil {
		if operation.RequestSHA256 != digest || operation.Kind != kind {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" || operation.Status == "outcome_unknown" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = now
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(readErr) {
		return durableMutationOperation{}, readErr
	}
	legacy, adoptedOK, err := s.adoptLegacyDurableMutationForRequest(ctx, operationID, projectID, kind, digest)
	if err != nil {
		return durableMutationOperation{}, err
	}
	if adoptedOK && !(freshAfterTerminal && durableMutationTerminal(legacy.Status)) {
		operation = legacy
		if operation.Status == "failed" || operation.Status == "outcome_unknown" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = now
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if adoptedOK {
		latest, found, findErr := s.findLatestEquivalentDurableMutation(ctx, kind, projectID, projectCode, sessionID, raw)
		if findErr != nil {
			return durableMutationOperation{}, findErr
		}
		if found && !durableMutationTerminal(latest.Status) {
			operation = latest
			operationID = latest.OperationID
			digest = latest.RequestSHA256
			if operation.Status == "failed" || operation.Status == "outcome_unknown" {
				operation.Status = "accepted"
				operation.Error = ""
				operation.UpdatedAt = now
				if err := s.writeDurableMutation(operation); err != nil {
					return durableMutationOperation{}, err
				}
			}
			s.startDurableMutationWorker()
			s.enqueueDurableMutation(operationID)
			return operation, nil
		}
		digest, err = freshDurableMutationDigest(kind, sessionID, raw)
		if err != nil {
			return durableMutationOperation{}, err
		}
		operationID, err = allocate(digest)
		if err != nil {
			return durableMutationOperation{}, err
		}
		if operation, readErr = s.readDurableMutation(operationID); readErr == nil {
			if operation.RequestSHA256 != digest || operation.Kind != kind {
				return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
			}
			s.startDurableMutationWorker()
			s.enqueueDurableMutation(operationID)
			return operation, nil
		} else if !os.IsNotExist(readErr) {
			return durableMutationOperation{}, readErr
		}
	}
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		MutationID:    digest,
		Kind:          kind,
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     projectID,
		Input:         raw,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if identityRaw != nil {
		operation.CapturedState = string(identityRaw)
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}

func durableMutationTerminal(status string) bool {
	return status == "completed"
}

func (s *Service) findLatestEquivalentDurableMutation(ctx context.Context, kind, projectID, projectCode, sessionID string, input []byte) (durableMutationOperation, bool, error) {
	inputSHA256 := durableMutationInputSHA256(input)
	localOperations, err := s.Durability.ListLocalOperationsByAdmissionCoordinate(ctx, projectID, kind, sessionID, inputSHA256)
	if err != nil {
		return durableMutationOperation{}, false, err
	}
	var latest durableMutationOperation
	found := false
	for _, local := range localOperations {
		if local.ProjectCode != projectCode || local.AdmissionSessionID != sessionID || local.AdmissionInputSHA256 != inputSHA256 {
			return durableMutationOperation{}, false, fmt.Errorf("equivalent Local operation %s admission coordinate mismatch", local.OperationID)
		}
		operation, readErr := s.readDurableMutation(local.OperationID)
		if readErr != nil {
			return durableMutationOperation{}, false, fmt.Errorf("equivalent Local operation %s is corrupt: %w", local.OperationID, readErr)
		}
		operationCode, operationNumber, parseErr := model.ParseOperationID(operation.OperationID)
		if parseErr != nil || operationCode != local.ProjectCode || operationNumber != local.OperationNumber || operation.OperationID != local.OperationID || operation.ProjectID != local.ProjectID || operation.Kind != local.Kind || operation.RequestSHA256 != local.MutationID {
			return durableMutationOperation{}, false, fmt.Errorf("equivalent Local operation %s identity mismatch", local.OperationID)
		}
		if operation.SessionID != sessionID {
			return durableMutationOperation{}, false, fmt.Errorf("equivalent Local operation %s session coordinate mismatch", local.OperationID)
		}
		if !durableMutationKnownStatus(operation.Status) {
			return durableMutationOperation{}, false, fmt.Errorf("equivalent Local operation %s has invalid status", local.OperationID)
		}
		equivalent, inputErr := durableMutationInputEqual(operation.Input, input)
		if inputErr != nil {
			return durableMutationOperation{}, false, fmt.Errorf("equivalent Local operation %s input is corrupt: %w", local.OperationID, inputErr)
		}
		if !equivalent {
			return durableMutationOperation{}, false, fmt.Errorf("equivalent Local operation %s admission input mismatch", local.OperationID)
		}
		if !found || durableMutationTurnAfter(operation, latest) {
			latest = operation
			found = true
		}
	}
	return latest, found, nil
}

func durableMutationKnownStatus(status string) bool {
	switch status {
	case "accepted", "running", "completed", "failed", "outcome_unknown":
		return true
	default:
		return false
	}
}

func durableMutationTurnAfter(candidate, current durableMutationOperation) bool {
	_, candidateNumber, candidateErr := model.ParseOperationID(candidate.OperationID)
	_, currentNumber, currentErr := model.ParseOperationID(current.OperationID)
	if candidateErr == nil && currentErr == nil && candidateNumber != currentNumber {
		return candidateNumber > currentNumber
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	return candidate.OperationID > current.OperationID
}

func durableMutationInputSHA256(input []byte) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, input); err == nil {
		input = compact.Bytes()
	}
	digest := sha256.Sum256(input)
	return hex.EncodeToString(digest[:])
}

func durableMutationInputEqual(left, right []byte) (bool, error) {
	var leftCompact, rightCompact bytes.Buffer
	if err := json.Compact(&leftCompact, left); err != nil {
		return false, err
	}
	if err := json.Compact(&rightCompact, right); err != nil {
		return false, err
	}
	return bytes.Equal(leftCompact.Bytes(), rightCompact.Bytes()), nil
}

func freshDurableMutationDigest(kind, sessionID string, input []byte) (string, error) {
	var turn [16]byte
	if _, err := rand.Read(turn[:]); err != nil {
		return "", fmt.Errorf("create server-owned Agent turn identity: %w", err)
	}
	return durableMutationDigestWithIdentity(kind, sessionID, input, turn[:]), nil
}

func (s *Service) localOperationProjectCode(ctx context.Context, projectID string) (string, error) {
	if s.Durability == nil || s.Durability.Local == nil {
		return "", fmt.Errorf("local durability is unavailable")
	}
	if project, ok := s.Config.Projects[projectID]; ok && project.ProjectCode != "" {
		if err := model.ValidateProjectCode(project.ProjectCode); err != nil {
			return "", err
		}
		return project.ProjectCode, nil
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" {
		if session, err := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID); err == nil && session.ProjectID == projectID && model.ValidateProjectCode(session.ProjectCode) == nil {
			return session.ProjectCode, nil
		}
	}
	if s.Durability == nil || s.Durability.Shared == nil {
		return "", fmt.Errorf("project %q has no local operation project code", projectID)
	}
	rows, err := s.Durability.Shared.Query(ctx, `SELECT project_code FROM shared_project_identifiers WHERE project_id=?`, projectID)
	if err != nil {
		return "", err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return "", fmt.Errorf("project %q has no local operation project code", projectID)
	}
	code, ok := rows.Rows[0][0].(string)
	if !ok || model.ValidateProjectCode(code) != nil {
		return "", fmt.Errorf("project %q has invalid local operation project code", projectID)
	}
	return code, nil
}
