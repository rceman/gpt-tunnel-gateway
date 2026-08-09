package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) adrCreateOnce(ctx context.Context, in ADRCreateInput) (OperationResult, error) {
	v := in.ADR
	v.SchemaVersion = model.SchemaVersion
	if v.ID != "" {
		return OperationResult{}, fmt.Errorf("ADR id is allocated by the gateway")
	}
	v.CreatedAt = time.Now().UTC()
	if v.Status == "" {
		v.Status = "accepted"
	}
	if _, err := s.ProjectRead(ctx, v.ProjectID); err != nil {
		return OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, v.ProjectID)
	if err != nil {
		return OperationResult{}, fmt.Errorf("read project identifiers: %w", err)
	}
	v.ID, err = model.FormatADRID(identifiers.ProjectCode, identifiers.NextADRNumber)
	if err != nil {
		return OperationResult{}, err
	}
	if identifiers.NextADRNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.adrPath(v.ProjectID, v.ID)); readErr == nil {
			return OperationResult{}, fmt.Errorf("ADR allocator exhausted for project %q", v.ProjectID)
		} else if !IsNotFound(readErr) {
			return OperationResult{}, readErr
		}
	}
	if err := model.ValidateADR(v); err != nil {
		return OperationResult{}, err
	}
	nextADR := identifiers.NextADRNumber
	if nextADR < model.MaxSafeInteger {
		nextADR++
	}
	updatedIdentifiers := identifiers
	updatedIdentifiers.NextADRNumber = nextADR
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create ADR "+v.ID, func(w string) ([]string, error) {
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(w, s.projectIdentifiersPath(v.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != identifiers.ProjectCode || current.NextADRNumber != identifiers.NextADRNumber {
			return nil, fmt.Errorf("project identifiers changed before ADR allocation")
		}
		path := s.adrPath(v.ProjectID, v.ID)
		if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("ADR already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := hub.WriteJSON(w, path, v); err != nil {
			return nil, err
		}
		identifiersPath := s.projectIdentifiersPath(v.ProjectID)
		if err := hub.WriteJSON(w, identifiersPath, updatedIdentifiers); err != nil {
			return nil, err
		}
		return []string{path, identifiersPath}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: v.ProjectID,
		Status:    "created",
	}, nil
}

func (s *Service) TaskCreate(ctx context.Context, in TaskCreateInput) (model.Task, OperationResult, error) {
	for attempt := 0; ; attempt++ {
		task, result, err := s.taskCreateOnce(ctx, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return task, result, err
		}
	}
}
