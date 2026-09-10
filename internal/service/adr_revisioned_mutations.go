package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) ADRUpdateCurrent(ctx context.Context, in ADRUpdateInput) (OperationResult, error) {
	current, err := s.readSharedADR(ctx, in.ProjectID, in.ADRID)
	if err != nil {
		return OperationResult{}, err
	}
	in.ExpectedRevision = current.Revision
	return s.ADRUpdate(ctx, in)
}

func (s *Service) ADRArchiveCurrent(ctx context.Context, in ADRArchiveInput) (OperationResult, error) {
	current, err := s.readSharedADR(ctx, in.ProjectID, in.ADRID)
	if err != nil {
		return OperationResult{}, err
	}
	in.ExpectedRevision = current.Revision
	return s.ADRArchive(ctx, in)
}

func (s *Service) adrHistorySeed(ctx context.Context, projectID, adrID string, revision int, previous model.ADR, payload []byte) (*sqlitestore.SharedHistorySeed, error) {
	if _, err := s.Durability.ReadSharedRevision(ctx, "adr", projectID, adrID, int64(revision)); err == nil {
		return nil, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	recordedAt := previous.UpdatedAt
	if recordedAt.IsZero() {
		recordedAt = previous.CreatedAt
	}
	if recordedAt.IsZero() {
		return nil, fmt.Errorf("ADR %s has no historical timestamp", adrID)
	}
	return &sqlitestore.SharedHistorySeed{
		Revision:      int64(revision),
		MutationKind:  "migration",
		Actor:         firstNonEmpty(previous.UpdatedBy, previous.CreatedBy, "migration"),
		Reason:        firstNonEmpty(previous.LastReason, "migration"),
		ChangedFields: []string{"migration"},
		Payload:       append([]byte(nil), payload...),
		RecordedAt:    recordedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (s *Service) adrRevisionOperationID(kind string, input any) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(kind+":"), raw...))
	return "adr-" + strings.TrimSuffix(hex.EncodeToString(digest[:]), ""), nil
}

func (s *Service) ADRUpdate(ctx context.Context, in ADRUpdateInput) (OperationResult, error) {
	if s.Durability == nil {
		return OperationResult{}, fmt.Errorf("ADR update requires Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ExpectedRevision < 1 || !validADRMutationReason(in.Reason) || strings.TrimSpace(in.UpdatedBy) == "" {
		return OperationResult{}, fmt.Errorf("ADR update requires expected_revision, reason, and updated_by")
	}
	if in.Title == nil && in.Context == nil && in.Decision == nil && in.Consequences == nil {
		return OperationResult{}, fmt.Errorf("ADR update requires at least one mutable content field")
	}
	id, _, err := parseADRSelector(in.ADRID, 0)
	if err != nil {
		return OperationResult{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "adr", id)
	if err != nil {
		return OperationResult{}, err
	}
	var current model.ADR
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		return OperationResult{}, err
	}
	current = normalizeADR(current)
	previous := current
	if current.ProjectID != in.ProjectID || current.ID != id {
		return OperationResult{}, fmt.Errorf("ADR ownership mismatch")
	}
	if current.Revision != in.ExpectedRevision {
		return OperationResult{}, fmt.Errorf("ADR revision conflict expected=%d actual=%d", in.ExpectedRevision, current.Revision)
	}
	if current.Status == model.ADRStatusArchived {
		return OperationResult{}, fmt.Errorf("archived ADR cannot be updated")
	}
	if in.Title != nil {
		current.Title = *in.Title
	}
	if in.Context != nil {
		current.Context = *in.Context
	}
	if in.Decision != nil {
		current.Decision = *in.Decision
	}
	if in.Consequences != nil {
		current.Consequences = *in.Consequences
	}
	if in.Status != nil {
		current.Status = *in.Status
	}
	current.Revision++
	current.RevisionCount = current.Revision
	current.UpdatedBy = strings.TrimSpace(in.UpdatedBy)
	current.UpdatedAt = s.durableNow()
	current.LastReason = strings.TrimSpace(in.Reason)
	if err := model.ValidateADR(current); err != nil {
		return OperationResult{}, err
	}
	payload, err := json.Marshal(current)
	if err != nil {
		return OperationResult{}, err
	}
	seed, err := s.adrHistorySeed(ctx, in.ProjectID, id, in.ExpectedRevision, previous, entity.Payload)
	if err != nil {
		return OperationResult{}, err
	}
	opID := durableMutationOperationID(ctx)
	if opID == "" {
		opID, err = s.adrRevisionOperationID("update", in)
		if err != nil {
			return OperationResult{}, err
		}
	}
	if _, err := s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{OperationID: opID, EntityType: "adr", ProjectID: in.ProjectID, EntityID: id, ExpectedRevision: int64(in.ExpectedRevision), ExpectedStoreRevision: entity.Revision, Revision: int64(current.Revision), Kind: "adr-update", HistoryMutationKind: "update", Payload: payload, Actor: current.UpdatedBy, Reason: current.LastReason, ChangedFields: changedADRFields(in), CreatedAt: current.UpdatedAt, PreviousHistory: seed}); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		OperationID: opID,
		ProjectID:   in.ProjectID,
		EntityKey:   id,
		Revision:    current.Revision,
		Status:      "updated",
		Hub:         hub.TransactionResult{Paths: []string{}},
	}, nil
}

func (s *Service) ADRArchive(ctx context.Context, in ADRArchiveInput) (OperationResult, error) {
	if s.Durability == nil {
		return OperationResult{}, fmt.Errorf("ADR archive requires Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ExpectedRevision < 1 || !validADRMutationReason(in.Reason) || strings.TrimSpace(in.ArchivedBy) == "" {
		return OperationResult{}, fmt.Errorf("ADR archive requires expected_revision, reason, and archived_by")
	}
	id, _, err := parseADRSelector(in.ADRID, 0)
	if err != nil {
		return OperationResult{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "adr", id)
	if err != nil {
		return OperationResult{}, err
	}
	var current model.ADR
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		return OperationResult{}, err
	}
	current = normalizeADR(current)
	previous := current
	if current.ProjectID != in.ProjectID || current.ID != id {
		return OperationResult{}, fmt.Errorf("ADR ownership mismatch")
	}
	if current.Revision != in.ExpectedRevision {
		return OperationResult{}, fmt.Errorf("ADR revision conflict expected=%d actual=%d", in.ExpectedRevision, current.Revision)
	}
	if current.Status == model.ADRStatusArchived {
		return OperationResult{}, fmt.Errorf("ADR already archived")
	}
	now := s.durableNow()
	current.Status = model.ADRStatusArchived
	current.Revision++
	current.RevisionCount = current.Revision
	current.UpdatedBy = strings.TrimSpace(in.ArchivedBy)
	current.UpdatedAt = now
	current.LastReason = strings.TrimSpace(in.Reason)
	current.ArchivedAt = &now
	current.ArchivedBy = current.UpdatedBy
	current.ArchiveReason = current.LastReason
	if err := model.ValidateADR(current); err != nil {
		return OperationResult{}, err
	}
	payload, err := json.Marshal(current)
	if err != nil {
		return OperationResult{}, err
	}
	seed, err := s.adrHistorySeed(ctx, in.ProjectID, id, in.ExpectedRevision, previous, entity.Payload)
	if err != nil {
		return OperationResult{}, err
	}
	opID := durableMutationOperationID(ctx)
	if opID == "" {
		opID, err = s.adrRevisionOperationID("archive", in)
		if err != nil {
			return OperationResult{}, err
		}
	}
	if _, err := s.Durability.CommitSharedLifecycleArchive(ctx, sqlitestore.SharedLifecycleArchive{SharedLifecycleRevision: sqlitestore.SharedLifecycleRevision{OperationID: opID, EntityType: "adr", ProjectID: in.ProjectID, EntityID: id, ExpectedRevision: int64(in.ExpectedRevision), ExpectedStoreRevision: entity.Revision, Revision: int64(current.Revision), Payload: payload, Actor: current.UpdatedBy, Reason: current.LastReason, ChangedFields: []string{"status", "archived_at", "archived_by", "archive_reason"}, CreatedAt: now, PreviousHistory: seed}}); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		OperationID: opID,
		ProjectID:   in.ProjectID,
		EntityKey:   id,
		Revision:    current.Revision,
		Status:      "archived",
		Hub:         hub.TransactionResult{Paths: []string{}},
	}, nil
}

func validADRMutationReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && len([]byte(reason)) <= 1024 && !strings.ContainsAny(reason, "\x00\r\n")
}
