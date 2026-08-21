package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type LocalOperationCreateInput struct {
	ProjectID     string `json:"project_id"`
	Kind          string `json:"kind"`
	CorrelationID string `json:"correlation_id,omitempty"`
	EntityID      string `json:"entity_id,omitempty"`
	RequestSHA256 string `json:"request_sha256,omitempty"`
}

type LocalOperationUpdateInput struct {
	ProjectID string `json:"project_id"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

type LocalOperationReadInput struct {
	ProjectID string `json:"project_id"`
	ID        string `json:"id"`
}

type localOperationCounter struct {
	Next uint64 `json:"next"`
}

func localOperationRoot(stateDir, projectID string) string {
	return filepath.Join(stateDir, "operations", "local", projectID)
}

func localOperationPath(stateDir, projectID, id string) string {
	return filepath.Join(localOperationRoot(stateDir, projectID), id+".json")
}

func localOperationCounterPath(stateDir, projectID string) string {
	return filepath.Join(localOperationRoot(stateDir, projectID), "counter.json")
}

func localOperationIndexPath(stateDir, projectID, key string) string {
	return filepath.Join(localOperationRoot(stateDir, projectID), "idempotency", key+".json")
}

func localOperationRequestKey(in LocalOperationCreateInput) string {
	if in.RequestSHA256 != "" {
		return in.RequestSHA256
	}
	hash := sha256.Sum256([]byte(in.Kind + "\x00" + in.CorrelationID + "\x00" + in.EntityID))
	return hex.EncodeToString(hash[:])
}

func (s *Service) LocalOperationCreate(_ context.Context, in LocalOperationCreateInput) (model.LocalOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.LocalOperation{}, err
	}
	if strings.TrimSpace(in.Kind) == "" || len(in.Kind) > 128 || strings.ContainsAny(in.Kind, "\x00\r\n") {
		return model.LocalOperation{}, fmt.Errorf("kind is required and bounded")
	}
	if strings.TrimSpace(in.CorrelationID) == "" {
		return model.LocalOperation{}, fmt.Errorf("correlation_id is required")
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil || project.ProjectCode == "" {
		return model.LocalOperation{}, fmt.Errorf("local project code is unavailable for %q", in.ProjectID)
	}
	if err := model.ValidateProjectCode(project.ProjectCode); err != nil {
		return model.LocalOperation{}, err
	}

	s.localOperationMu.Lock()
	defer s.localOperationMu.Unlock()
	key := localOperationRequestKey(in)
	indexPath := localOperationIndexPath(s.Config.StateDir, in.ProjectID, key)
	var existingID string
	if err := fsutil.ReadJSONBounded(indexPath, 4096, &existingID); err == nil && existingID != "" {
		return s.readLocalOperationLocked(in.ProjectID, existingID, project.ProjectCode)
	} else if err != nil && !os.IsNotExist(err) {
		return model.LocalOperation{}, err
	}

	counter := localOperationCounter{Next: 1}
	if err := fsutil.ReadJSONBounded(localOperationCounterPath(s.Config.StateDir, in.ProjectID), 4096, &counter); err != nil && !os.IsNotExist(err) {
		return model.LocalOperation{}, err
	}
	if counter.Next == 0 || counter.Next > model.MaxSafeInteger {
		return model.LocalOperation{}, fmt.Errorf("local operation counter exhausted")
	}
	id, err := model.FormatOperatorEventID(project.ProjectCode, counter.Next)
	if err != nil {
		return model.LocalOperation{}, err
	}
	now := time.Now().UTC()
	operation := model.LocalOperation{SchemaVersion: model.LocalOperationSchemaVersion, ID: id, ProjectID: in.ProjectID, Kind: in.Kind, Status: "accepted", CorrelationID: in.CorrelationID, EntityID: in.EntityID, RequestSHA256: key, CreatedAt: now, UpdatedAt: now}
	if err := model.ValidateLocalOperation(operation); err != nil {
		return model.LocalOperation{}, err
	}
	if err := fsutil.WriteJSONAtomic(localOperationPath(s.Config.StateDir, in.ProjectID, id), operation, 0o600); err != nil {
		return model.LocalOperation{}, err
	}
	if err := fsutil.WriteJSONAtomic(indexPath, id, 0o600); err != nil {
		return model.LocalOperation{}, err
	}
	counter.Next++
	if err := fsutil.WriteJSONAtomic(localOperationCounterPath(s.Config.StateDir, in.ProjectID), counter, 0o600); err != nil {
		return model.LocalOperation{}, err
	}
	return operation, nil
}

func (s *Service) LocalOperationUpdate(_ context.Context, in LocalOperationUpdateInput) (model.LocalOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.LocalOperation{}, err
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil || project.ProjectCode == "" {
		return model.LocalOperation{}, fmt.Errorf("local project code is unavailable for %q", in.ProjectID)
	}
	s.localOperationMu.Lock()
	defer s.localOperationMu.Unlock()
	operation, err := s.readLocalOperationLocked(in.ProjectID, in.ID, project.ProjectCode)
	if err != nil {
		return model.LocalOperation{}, err
	}
	operation.Status, operation.Result, operation.Error, operation.UpdatedAt = in.Status, in.Result, in.Error, time.Now().UTC()
	if err := model.ValidateLocalOperation(operation); err != nil {
		return model.LocalOperation{}, err
	}
	if err := fsutil.WriteJSONAtomic(localOperationPath(s.Config.StateDir, in.ProjectID, in.ID), operation, 0o600); err != nil {
		return model.LocalOperation{}, err
	}
	return operation, nil
}

func (s *Service) LocalOperationRead(_ context.Context, in LocalOperationReadInput) (model.LocalOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.LocalOperation{}, err
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil || project.ProjectCode == "" {
		return model.LocalOperation{}, fmt.Errorf("local project code is unavailable for %q", in.ProjectID)
	}
	s.localOperationMu.Lock()
	defer s.localOperationMu.Unlock()
	return s.readLocalOperationLocked(in.ProjectID, in.ID, project.ProjectCode)
}

func (s *Service) readLocalOperationLocked(projectID, id, projectCode string) (model.LocalOperation, error) {
	if err := model.ValidateOperatorEventID(id); err != nil {
		return model.LocalOperation{}, err
	}
	code, _, _ := model.ParseOperatorEventID(id)
	if code != projectCode {
		return model.LocalOperation{}, fmt.Errorf("operation does not belong to project")
	}
	var operation model.LocalOperation
	if err := fsutil.ReadJSONBounded(localOperationPath(s.Config.StateDir, projectID, id), 1<<20, &operation); err != nil {
		return model.LocalOperation{}, err
	}
	if err := model.ValidateLocalOperation(operation); err != nil {
		return model.LocalOperation{}, err
	}
	if operation.ProjectID != projectID || operation.ProjectCode() != projectCode {
		return model.LocalOperation{}, fmt.Errorf("local operation ownership mismatch")
	}
	return operation, nil
}
