package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	adr = normalizeADR(adr)
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
		adr = normalizeADR(adr)
		if err := model.ValidateADR(adr); err != nil {
			return nil, err
		}
		items = append(items, adr)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

type sharedADRPage struct {
	ADRs       []model.ADR
	NextCursor string
	HasMore    bool
	CursorKind string
}

func (s *Service) querySharedADRs(ctx context.Context, projectID, text, status string, includeArchived bool, limit int, cursor string) (sharedADRPage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return sharedADRPage{}, err
	}
	filters := map[string]string{}
	if status != "" {
		filters["status"] = status
	}
	includeArchived = includeArchived || status == model.ADRStatusArchived || status == model.ADRStatusSuperseded
	entities, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{
		EntityType: "adr", ProjectID: projectID, Text: text, Filters: filters,
		IncludeArchived: includeArchived, ExcludeSuperseded: includeArchived && status == "", Limit: limit, Cursor: cursor,
	})
	if err != nil {
		return sharedADRPage{}, err
	}
	items := make([]model.ADR, 0, len(entities.Entities))
	for _, entity := range entities.Entities {
		var adr model.ADR
		if err := json.Unmarshal(entity.Payload, &adr); err != nil {
			return sharedADRPage{}, fmt.Errorf("decode shared ADR %s: %w", entity.ID, err)
		}
		if adr.ProjectID != projectID || adr.ID != entity.ID {
			return sharedADRPage{}, fmt.Errorf("shared ADR identity mismatch")
		}
		adr = normalizeADR(adr)
		if err := model.ValidateADR(adr); err != nil {
			return sharedADRPage{}, err
		}
		items = append(items, adr)
	}
	return sharedADRPage{
		ADRs:       items,
		NextCursor: entities.NextCursor,
		HasMore:    entities.HasMore,
		CursorKind: entities.CursorKind,
	}, nil
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
	_, id, _, err := s.Durability.CommitSharedLifecycleCreate(ctx, sqlitestore.SharedLifecycleCreate{
		OperationID:         operationID,
		EntityType:          "adr",
		ProjectID:           in.ADR.ProjectID,
		ProjectCode:         project.ProjectCode,
		InitialNextNumber:   1,
		Kind:                "adr-create",
		HistoryMutationKind: "create",
		Actor:               firstNonEmpty(in.ADR.CreatedBy, "server"),
		Reason:              "create",
		ChangedFields:       []string{"title", "context", "decision", "consequences"},
		CreatedAt:           time.Now().UTC(),
		BuildPayload: func(adrID string) ([]byte, error) {
			created = in.ADR
			created.SchemaVersion = model.SchemaVersion
			created.ID = adrID
			created.CreatedAt = time.Now().UTC()
			created.Revision = 1
			created.RevisionCount = 1
			created.UpdatedAt = time.Time{}
			created.LastReason = "create"
			payload, err := json.Marshal(created)
			if err != nil {
				return nil, err
			}
			payload, err = sqlitestore.ApplySharedLifecycleCreateDefaults("adr", payload)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(payload, &created); err != nil {
				return nil, err
			}
			if err := model.ValidateADR(created); err != nil {
				return nil, err
			}
			return payload, nil
		},
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		OperationID: operationID,
		ProjectID:   created.ProjectID,
		EntityKey:   id,
		Revision:    1,
		Status:      "created",
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeADR(adr model.ADR) model.ADR {
	if adr.Status == "" {
		adr.Status = model.ADRStatusAccepted
	}
	if adr.Revision == 0 {
		adr.Revision = 1
	}
	if adr.RevisionCount < adr.Revision {
		adr.RevisionCount = adr.Revision
	}
	if adr.CreatedBy == "" {
		adr.CreatedBy = "migration"
	}
	if adr.UpdatedBy == "" {
		adr.UpdatedBy = adr.CreatedBy
	}
	if adr.UpdatedAt.IsZero() {
		adr.UpdatedAt = adr.CreatedAt
	}
	if adr.LastReason == "" {
		adr.LastReason = "init"
	}
	if adr.Status == model.ADRStatusArchived {
		if adr.ArchivedAt == nil && !adr.UpdatedAt.IsZero() {
			at := adr.UpdatedAt
			adr.ArchivedAt = &at
		}
		if adr.ArchivedBy == "" {
			adr.ArchivedBy = adr.UpdatedBy
		}
		if adr.ArchiveReason == "" {
			adr.ArchiveReason = adr.LastReason
		}
	}
	return adr
}
