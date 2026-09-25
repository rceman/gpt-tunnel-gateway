package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const sharedHubRestoreMaxEntities = 4096

type sharedHubEntityDecoder func([]byte) (sqlitestore.SharedEntity, string, error)

func decodeHubSharedEntity[T any](data []byte, projectID string, validate func(T) error, identity func(T) (string, int64, time.Time), path func(T) string) (sqlitestore.SharedEntity, string, error) {
	var value T
	if err := decodeStrict(data, &value); err != nil {
		return sqlitestore.SharedEntity{}, "", err
	}
	if err := validate(value); err != nil {
		return sqlitestore.SharedEntity{}, "", err
	}
	id, revision, updatedAt := identity(value)
	if id == "" || revision < 1 || updatedAt.IsZero() {
		return sqlitestore.SharedEntity{}, "", fmt.Errorf("invalid portable entity metadata")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return sqlitestore.SharedEntity{}, "", err
	}
	return sqlitestore.SharedEntity{ID: id, Revision: revision, Payload: payload, UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano)}, path(value), nil
}

func sameCanonicalJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func (s *Service) restoreHubEntityFamily(ctx context.Context, snapshot *hub.ReadSnapshot, entityType, prefix string, decode sharedHubEntityDecoder) error {
	paths, err := snapshot.List(ctx, prefix, ".json")
	if err != nil {
		return err
	}
	if len(paths) > sharedHubRestoreMaxEntities {
		return fmt.Errorf("Hub %s restore exceeds bounded entity maximum", entityType)
	}
	for _, path := range paths {
		data, err := snapshot.ReadFile(ctx, path)
		if err != nil {
			return err
		}
		entity, expectedPath, err := decode(data)
		if err != nil {
			return fmt.Errorf("decode Hub %s %q: %w", entityType, path, err)
		}
		if path != expectedPath {
			return fmt.Errorf("Hub %s path conflicts with entity identity", entityType)
		}
		current, err := s.Durability.ReadSharedEntity(ctx, entityType, entity.ID)
		if err == nil {
			switch {
			case current.Revision > entity.Revision:
				continue
			case current.Revision == entity.Revision:
				if !sameCanonicalJSON(current.Payload, entity.Payload) {
					return fmt.Errorf("Shared %s %s conflicts with Hub revision %d", entityType, entity.ID, entity.Revision)
				}
				continue
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.Durability.PutSharedProjection(ctx, entityType, entity); err != nil {
			return err
		}
	}
	return nil
}

func validatePortableRevisionPayload(record portableSharedRevision) error {
	switch record.EntityType {
	case "task":
		var value model.TaskAuthoring
		if err := decodeStrict(record.Payload, &value); err != nil || model.ValidateTaskAuthoring(value) != nil || value.ID != record.EntityID || value.ProjectID != record.ProjectID {
			return fmt.Errorf("Task revision payload identity mismatch")
		}
	case "adr":
		var value model.ADR
		if err := decodeStrict(record.Payload, &value); err != nil || model.ValidateADR(value) != nil || value.ID != record.EntityID || value.ProjectID != record.ProjectID || int64(value.Revision) != record.Revision {
			return fmt.Errorf("ADR revision payload identity mismatch")
		}
	case "rule":
		var value model.Rule
		if err := decodeStrict(record.Payload, &value); err != nil || model.ValidateRule(value) != nil || value.ID != record.EntityID || value.ProjectID != record.ProjectID || int64(value.Revision) != record.Revision {
			return fmt.Errorf("Rule revision payload identity mismatch")
		}
	case "milestone":
		var value model.Milestone
		if err := decodeStrict(record.Payload, &value); err != nil || model.ValidateMilestone(value) != nil || value.ID != record.EntityID || value.ProjectID != record.ProjectID || int64(value.Revision) != record.Revision {
			return fmt.Errorf("Milestone revision payload identity mismatch")
		}
	case "track":
		var value model.Track
		if err := decodeStrict(record.Payload, &value); err != nil || model.ValidateTrack(value) != nil || value.ID != record.EntityID || value.ProjectID != record.ProjectID || int64(value.Revision) != record.Revision {
			return fmt.Errorf("Track revision payload identity mismatch")
		}
	case "journal":
		var value model.JournalEntry
		if err := decodeStrict(record.Payload, &value); err != nil || model.ValidateJournalEntry(value) != nil || value.ID != record.EntityID || value.ProjectID != record.ProjectID || record.Revision != 1 {
			return fmt.Errorf("Journal revision payload identity mismatch")
		}
	default:
		return fmt.Errorf("unsupported portable revision entity type %q", record.EntityType)
	}
	return nil
}

func validatePortableLifecycleEvent(event portableSharedLifecycleEvent, projectID string) error {
	if model.ValidateProjectIdentifier(projectID) != nil || event.ProjectID != projectID || event.OperationID == "" || len(event.OperationID) > 256 || strings.ContainsAny(event.OperationID, "\x00\r\n") || event.Revision < 1 || event.FromStatus == "" || event.FromStatus == event.ToStatus || event.ToStatus == "" || event.MutationKind == "" || event.Actor == "" || event.Reason == "" || len(event.ChangedFields) == 0 || !json.Valid(event.Contract) {
		return fmt.Errorf("invalid portable lifecycle event metadata")
	}
	if _, err := time.Parse(time.RFC3339Nano, event.RecordedAt); err != nil {
		return fmt.Errorf("invalid portable lifecycle event timestamp")
	}
	if model.ValidateObjectIdentifier(event.EntityID) != nil {
		return fmt.Errorf("invalid portable lifecycle event entity identity")
	}
	switch event.EntityType {
	case "task", "adr", "rule", "milestone", "track":
	default:
		return fmt.Errorf("unsupported portable lifecycle event entity type %q", event.EntityType)
	}
	if event.EventKind != sqlitestore.SharedLifecycleEventKindStatus && event.EventKind != sqlitestore.SharedLifecycleEventKindArchive {
		return fmt.Errorf("invalid portable lifecycle event kind")
	}
	return nil
}

func (s *Service) restoreHubRevisionFamily(ctx context.Context, snapshot *hub.ReadSnapshot, projectID, entityType string) error {
	prefix := s.projectPrefix(projectID) + "/entity-revisions/" + entityType
	paths, err := snapshot.List(ctx, prefix, ".json")
	if err != nil {
		return err
	}
	if len(paths) > sharedHubRestoreMaxEntities*4 {
		return fmt.Errorf("Hub %s revision restore exceeds bounded record maximum", entityType)
	}
	for _, path := range paths {
		data, err := snapshot.ReadFile(ctx, path)
		if err != nil {
			return err
		}
		var portable portableSharedRevision
		if err := decodeStrict(data, &portable); err != nil {
			return fmt.Errorf("decode Hub %s revision %q: %w", entityType, path, err)
		}
		if portable.EntityType != entityType || portable.ProjectID != projectID || portable.Revision < 1 || portable.MutationKind == "" || portable.Actor == "" || portable.Reason == "" || portable.RecordedAt == "" || !json.Valid(portable.Payload) || s.sharedRevisionPath(projectID, entityType, portable.EntityID, portable.Revision) != path {
			return fmt.Errorf("Hub %s revision identity is invalid", entityType)
		}
		if err := validatePortableRevisionPayload(portable); err != nil {
			return err
		}
		var compactPayload bytes.Buffer
		if err := json.Compact(&compactPayload, portable.Payload); err != nil {
			return fmt.Errorf("Hub %s revision payload is invalid", entityType)
		}
		current, err := s.Durability.ReadSharedEntity(ctx, entityType, portable.EntityID)
		if err != nil {
			return err
		}
		if current.Revision < portable.Revision {
			return fmt.Errorf("Hub %s revision exceeds restored current state", entityType)
		}
		record := sqlitestore.SharedRevisionRecord{
			EntityID: portable.EntityID, ProjectID: portable.ProjectID, Revision: portable.Revision,
			MutationKind: portable.MutationKind, Actor: portable.Actor, Reason: portable.Reason,
			ChangedFields: portable.ChangedFields, Payload: append([]byte(nil), compactPayload.Bytes()...), RecordedAt: portable.RecordedAt,
		}
		if err := s.Durability.EnsureSharedLifecycleHistory(ctx, entityType, record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restoreHubLifecycleEvents(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	prefix := s.projectPrefix(projectID) + "/lifecycle-events"
	paths, err := snapshot.List(ctx, prefix, ".json")
	if err != nil {
		return err
	}
	if len(paths) > sharedHubRestoreMaxEntities*4 {
		return fmt.Errorf("Hub lifecycle event restore exceeds bounded record maximum")
	}
	events := make([]portableSharedLifecycleEvent, 0, len(paths))
	for _, path := range paths {
		data, err := snapshot.ReadFile(ctx, path)
		if err != nil {
			return err
		}
		var event portableSharedLifecycleEvent
		if err := decodeStrict(data, &event); err != nil {
			return fmt.Errorf("decode Hub lifecycle event %q: %w", path, err)
		}
		if err := validatePortableLifecycleEvent(event, projectID); err != nil || s.sharedLifecycleEventPath(projectID, event.EntityType, event.EntityID, event.OperationID) != path {
			return fmt.Errorf("Hub lifecycle event path or identity is invalid")
		}
		at, err := time.Parse(time.RFC3339Nano, event.RecordedAt)
		if err != nil || at.UTC().Format(time.RFC3339Nano) != event.RecordedAt {
			return fmt.Errorf("Hub lifecycle event timestamp is not canonical")
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		left, right := events[i], events[j]
		if left.EntityType != right.EntityType {
			return left.EntityType < right.EntityType
		}
		if left.EntityID != right.EntityID {
			return left.EntityID < right.EntityID
		}
		if left.RecordedAt != right.RecordedAt {
			return left.RecordedAt < right.RecordedAt
		}
		return left.OperationID < right.OperationID
	})
	entities := make(map[string]struct{})
	for _, event := range events {
		at, _ := time.Parse(time.RFC3339Nano, event.RecordedAt)
		stored := sqlitestore.SharedLifecycleEvent{
			OperationID: event.OperationID, EntityType: event.EntityType, ProjectID: event.ProjectID,
			EntityID: event.EntityID, Revision: event.Revision, EventKind: event.EventKind,
			MutationKind: event.MutationKind, FromStatus: event.FromStatus, ToStatus: event.ToStatus,
			Actor: event.Actor, Reason: event.Reason, Contract: append([]byte(nil), event.Contract...),
			ChangedFields: append([]string(nil), event.ChangedFields...), RecordedAt: at,
		}
		if err := s.Durability.EnsureSharedLifecycleEvent(ctx, stored); err != nil {
			return err
		}
		entities[event.EntityType+"\x00"+event.EntityID] = struct{}{}
	}
	for identity := range entities {
		parts := strings.SplitN(identity, "\x00", 2)
		if _, err := s.Durability.ListSharedLifecycleEvents(ctx, parts[0], projectID, parts[1], sqlitestore.SharedLifecycleQueryMaxRows); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restoreHubCanonicalSequences(ctx context.Context, snapshot *hub.ReadSnapshot, projectID, projectCode string) error {
	identifiersData, err := snapshot.ReadFile(ctx, s.projectIdentifiersPath(projectID))
	if err != nil {
		return fmt.Errorf("read Hub project identifiers for sequence restore: %w", err)
	}
	var identifiers model.ProjectIdentifiers
	if err := decodeStrict(identifiersData, &identifiers); err != nil || model.ValidateProjectIdentifiers(identifiers) != nil || identifiers.ProjectID != projectID || identifiers.ProjectCode != projectCode {
		return fmt.Errorf("Hub project identifiers are invalid for sequence restore")
	}
	for _, sequence := range []struct {
		entityType string
		next       uint64
	}{
		{"task", identifiers.NextTaskNumber}, {"adr", identifiers.NextADRNumber},
	} {
		if sequence.next > model.MaxSafeInteger {
			return fmt.Errorf("Hub %s sequence exceeds the safe identifier range", sequence.entityType)
		}
		if err := s.Durability.ReconcileSharedSequence(ctx, sequence.entityType, projectID, projectCode, int64(sequence.next)); err != nil {
			return fmt.Errorf("restore Hub %s sequence: %w", sequence.entityType, err)
		}
	}
	counterPath := s.projectPrefix(projectID) + "/operator-journal/counter.json"
	counterData, err := snapshot.ReadFile(ctx, counterPath)
	if err != nil {
		if !IsNotFound(err) {
			return err
		}
	} else {
		var counter model.OperatorJournalCounter
		if err := decodeStrict(counterData, &counter); err != nil || model.ValidateOperatorJournalCounter(counter) != nil || counter.ProjectID != projectID || counter.NextEventNumber > model.MaxSafeInteger {
			return fmt.Errorf("Hub operator Journal counter is invalid for sequence restore")
		}
		if err := s.Durability.ReconcileSharedSequence(ctx, "journal", projectID, projectCode, int64(counter.NextEventNumber)); err != nil {
			return fmt.Errorf("restore Hub Journal counter: %w", err)
		}
	}
	return nil
}

func (s *Service) restoreHubProjectSemantics(ctx context.Context, projectID, projectCode string) error {
	if s.Durability == nil {
		return fmt.Errorf("Shared durability is required for portable restore")
	}
	snapshot, err := s.Hub.ReadSnapshot(ctx)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	prefix := s.projectPrefix(projectID)
	families := []struct {
		entityType string
		path       string
		decode     sharedHubEntityDecoder
	}{
		{"task", prefix + "/tasks-v2", func(data []byte) (sqlitestore.SharedEntity, string, error) {
			return decodeHubSharedEntity(data, projectID, func(v model.TaskAuthoring) error {
				if v.ProjectID != projectID {
					return fmt.Errorf("Task project identity mismatch")
				}
				return model.ValidateTaskAuthoring(v)
			}, func(v model.TaskAuthoring) (string, int64, time.Time) { return v.ID, int64(v.Revision), v.UpdatedAt }, func(v model.TaskAuthoring) string { return s.taskAuthoringPath(projectID, v.ID) })
		}},
		{"adr", prefix + "/adrs", func(data []byte) (sqlitestore.SharedEntity, string, error) {
			return decodeHubSharedEntity(data, projectID, func(v model.ADR) error {
				if v.ProjectID != projectID {
					return fmt.Errorf("ADR project identity mismatch")
				}
				return model.ValidateADR(v)
			}, func(v model.ADR) (string, int64, time.Time) { return v.ID, int64(v.Revision), v.UpdatedAt }, func(v model.ADR) string { return s.adrPath(projectID, v.ID) })
		}},
		{"rule", prefix + "/rules", func(data []byte) (sqlitestore.SharedEntity, string, error) {
			return decodeHubSharedEntity(data, projectID, func(v model.Rule) error {
				if v.ProjectID != projectID {
					return fmt.Errorf("Rule project identity mismatch")
				}
				return model.ValidateRule(v)
			}, func(v model.Rule) (string, int64, time.Time) { return v.ID, int64(v.Revision), v.UpdatedAt }, func(v model.Rule) string { return s.rulePath(projectID, v.ID) })
		}},
		{"milestone", prefix + "/milestones", func(data []byte) (sqlitestore.SharedEntity, string, error) {
			return decodeHubSharedEntity(data, projectID, func(v model.Milestone) error {
				if v.ProjectID != projectID {
					return fmt.Errorf("Milestone project identity mismatch")
				}
				return model.ValidateMilestone(v)
			}, func(v model.Milestone) (string, int64, time.Time) { return v.ID, int64(v.Revision), v.UpdatedAt }, func(v model.Milestone) string { return s.milestonePath(projectID, v.ID) })
		}},
		{"track", prefix + "/tracks", func(data []byte) (sqlitestore.SharedEntity, string, error) {
			return decodeHubSharedEntity(data, projectID, func(v model.Track) error {
				if v.ProjectID != projectID {
					return fmt.Errorf("Track project identity mismatch")
				}
				return model.ValidateTrack(v)
			}, func(v model.Track) (string, int64, time.Time) { return v.ID, int64(v.Revision), v.UpdatedAt }, func(v model.Track) string { return s.trackPath(projectID, v.ID) })
		}},
		{"journal", prefix + "/journals", func(data []byte) (sqlitestore.SharedEntity, string, error) {
			return decodeHubSharedEntity(data, projectID, func(v model.JournalEntry) error {
				if v.ProjectID != projectID {
					return fmt.Errorf("Journal project identity mismatch")
				}
				return model.ValidateJournalEntry(v)
			}, func(v model.JournalEntry) (string, int64, time.Time) { return v.ID, 1, v.CreatedAt }, func(v model.JournalEntry) string { return s.journalPath(projectID, v.ID) })
		}},
	}
	for _, family := range families {
		if err := s.restoreHubEntityFamily(ctx, snapshot, family.entityType, family.path, family.decode); err != nil {
			return fmt.Errorf("restore Hub %s: %w", family.entityType, err)
		}
	}
	for _, entityType := range []string{"task", "adr", "rule", "journal", "milestone", "track"} {
		if err := s.restoreHubRevisionFamily(ctx, snapshot, projectID, entityType); err != nil {
			return fmt.Errorf("restore Hub %s revision evidence: %w", entityType, err)
		}
	}
	if err := s.restoreHubLifecycleEvents(ctx, snapshot, projectID); err != nil {
		return fmt.Errorf("restore Hub lifecycle evidence: %w", err)
	}
	relationPrefix := prefix + "/relations"
	relationPaths, err := snapshot.List(ctx, relationPrefix, ".json")
	if err != nil {
		return err
	}
	if len(relationPaths) > sharedHubRestoreMaxEntities {
		return fmt.Errorf("Hub relation restore exceeds bounded entity maximum")
	}
	for _, path := range relationPaths {
		data, err := snapshot.ReadFile(ctx, path)
		if err != nil {
			return err
		}
		var relation model.Relation
		if err := decodeStrict(data, &relation); err != nil {
			return fmt.Errorf("decode Hub relation %q: %w", path, err)
		}
		if relation.ProjectID != projectID || model.ValidateRelation(relation) != nil || s.relationPath(relation) != path {
			return fmt.Errorf("Hub relation path or identity is invalid")
		}
		if family, err := model.RelationFamilyOf(relation.Source); err != nil || family == model.RelationFamilyPMT {
			return fmt.Errorf("Hub relation contains Local-only endpoint")
		}
		if err := s.Durability.EnsureSharedRelation(ctx, relation); err != nil {
			return err
		}
	}
	if err := s.restoreHubCanonicalSequences(ctx, snapshot, projectID, projectCode); err != nil {
		return err
	}
	return s.Durability.ReconstructSharedEntitySequences(ctx, projectID, projectCode)
}
