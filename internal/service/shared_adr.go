package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type durableMutationOperationIDKey struct{}

func (s *Service) readSharedADR(ctx context.Context, projectID, id string) (model.ADR, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.ADR{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "adr", id)
	if err != nil {
		return model.ADR{}, err
	}
	var adr model.ADR
	if err := json.Unmarshal(entity.Payload, &adr); err != nil {
		return model.ADR{}, fmt.Errorf("decode shared ADR %s: %w", id, err)
	}
	if adr.ID != id || adr.ProjectID != projectID {
		return model.ADR{}, fmt.Errorf("shared ADR ownership mismatch")
	}
	if err := model.ValidateADR(adr); err != nil {
		return model.ADR{}, err
	}
	return adr, nil
}

func (s *Service) listSharedADRs(ctx context.Context, projectID string) ([]model.ADR, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return nil, err
	}
	entities, err := s.sharedProjectEntities(ctx, "adr", projectID)
	if err != nil {
		return nil, err
	}
	items := make([]model.ADR, 0, len(entities))
	for _, entity := range entities {
		var adr model.ADR
		if err := json.Unmarshal(entity.Payload, &adr); err != nil {
			return nil, fmt.Errorf("decode shared ADR %s: %w", entity.ID, err)
		}
		if adr.ProjectID != projectID {
			continue
		}
		if adr.ID != entity.ID {
			return nil, fmt.Errorf("shared ADR identity mismatch")
		}
		if err := model.ValidateADR(adr); err != nil {
			return nil, err
		}
		items = append(items, adr)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func withDurableMutationOperationID(ctx context.Context, operationID string) context.Context {
	return context.WithValue(ctx, durableMutationOperationIDKey{}, operationID)
}

func durableMutationOperationID(ctx context.Context) string {
	value, _ := ctx.Value(durableMutationOperationIDKey{}).(string)
	return value
}

func (s *Service) adrCreateShared(ctx context.Context, in ADRCreateInput) (OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ADR.ProjectID); err != nil {
		return OperationResult{}, err
	}
	project, ok := s.Config.Projects[in.ADR.ProjectID]
	if !ok || model.ValidateProjectCode(project.ProjectCode) != nil {
		return OperationResult{}, fmt.Errorf("project %q has no local project code", in.ADR.ProjectID)
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		encoded, err := json.Marshal(in)
		if err != nil {
			return OperationResult{}, err
		}
		digest := sha256.Sum256(encoded)
		operationID = "adr-shared-" + hex.EncodeToString(digest[:])
	}
	var created model.ADR
	_, id, _, err := s.Durability.CommitSharedADRCreate(ctx, sqlitestore.SharedADRCreate{
		OperationID:          operationID,
		ProjectID:            in.ADR.ProjectID,
		ProjectCode:          project.ProjectCode,
		InitialNextADRNumber: 1,
		Kind:                 "adr-create",
		CreatedAt:            time.Now().UTC(),
		BuildPayload: func(adrID string) ([]byte, error) {
			created = in.ADR
			created.SchemaVersion = model.SchemaVersion
			created.ID = adrID
			created.CreatedAt = time.Now().UTC()
			if created.Status == "" {
				created.Status = "accepted"
			}
			if err := model.ValidateADR(created); err != nil {
				return nil, err
			}
			return json.Marshal(created)
		},
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{OperationID: operationID, ProjectID: created.ProjectID, Status: "created", TaskID: id}, nil
}
