package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

var errSharedOutboxNoop = errors.New("shared outbox publication is already current")

type portableSharedRevision struct {
	EntityType    string          `json:"entity_type"`
	EntityID      string          `json:"entity_id"`
	ProjectID     string          `json:"project_id"`
	Revision      int64           `json:"revision"`
	MutationKind  string          `json:"mutation_kind"`
	Actor         string          `json:"actor"`
	Reason        string          `json:"reason"`
	ChangedFields []string        `json:"changed_fields"`
	Payload       json.RawMessage `json:"payload"`
	RecordedAt    string          `json:"recorded_at"`
}

type portableSharedLifecycleEvent struct {
	OperationID   string          `json:"operation_id"`
	EntityType    string          `json:"entity_type"`
	ProjectID     string          `json:"project_id"`
	EntityID      string          `json:"entity_id"`
	Revision      int64           `json:"revision"`
	EventKind     string          `json:"event_kind"`
	MutationKind  string          `json:"mutation_kind"`
	FromStatus    string          `json:"from_status"`
	ToStatus      string          `json:"to_status"`
	Actor         string          `json:"actor"`
	Reason        string          `json:"reason"`
	Contract      json.RawMessage `json:"contract"`
	ChangedFields []string        `json:"changed_fields"`
	RecordedAt    string          `json:"recorded_at"`
}

func samePortableLifecycleEvent(left, right portableSharedLifecycleEvent) bool {
	return left.OperationID == right.OperationID && left.EntityType == right.EntityType && left.ProjectID == right.ProjectID && left.EntityID == right.EntityID && left.Revision == right.Revision && left.EventKind == right.EventKind && left.MutationKind == right.MutationKind && left.FromStatus == right.FromStatus && left.ToStatus == right.ToStatus && left.Actor == right.Actor && left.Reason == right.Reason && sameCanonicalJSON(left.Contract, right.Contract) && reflect.DeepEqual(left.ChangedFields, right.ChangedFields) && left.RecordedAt == right.RecordedAt
}

func (s *Service) startSharedOutboxWorker() {
	if s.Durability == nil {
		return
	}
	s.sharedOutboxWorkerOnce.Do(func() { go s.sharedOutboxWorker() })
}

func (s *Service) sharedOutboxWorker() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		entries, err := s.Durability.PendingOutbox(ctx, 32)
		cancel()
		if err == nil {
			for _, entry := range entries {
				workerCtx, workerCancel := s.asyncMutationContext("shared-outbox", entry.ID)
				_ = s.deliverSharedOutboxEntry(workerCtx, entry)
				workerCancel()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *Service) deliverSharedOutboxEntry(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	publicationErr := s.publishSharedOutboxEntry(ctx, entry)
	if publicationErr == nil || errors.Is(publicationErr, errSharedOutboxNoop) {
		return s.Durability.MarkOutboxPublished(context.Background(), entry.ID, time.Now().UTC())
	}
	if err := s.Durability.MarkOutboxRetry(context.Background(), entry.ID, time.Now().UTC().Add(sharedOutboxRetryDelay(entry.Attempts+1)), publicationErr); err != nil {
		return fmt.Errorf("publish shared outbox %s: %v; record retry: %w", entry.ID, publicationErr, err)
	}
	return publicationErr
}

func sharedOutboxRetryDelay(attempt int64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

func (s *Service) publishSharedOutboxEntry(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	publisher, ok := sharedOutboxPublishers[entry.EntityType]
	if !ok {
		return fmt.Errorf("unsupported shared outbox entity %q", entry.EntityType)
	}
	if err := s.publishSharedRevisionOutbox(ctx, entry); err != nil && !errors.Is(err, errSharedOutboxNoop) {
		return err
	}
	if err := s.publishSharedLifecycleEventsOutbox(ctx, entry); err != nil && !errors.Is(err, errSharedOutboxNoop) {
		return err
	}
	return publisher(s, ctx, entry)
}

func (s *Service) publishSharedRevisionOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	if s.Durability == nil || entry.EntityType == "relation" || entry.EntityType == "project_configuration" {
		return nil
	}
	var identity struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(entry.Payload, &identity); err != nil || identity.ProjectID == "" {
		return fmt.Errorf("invalid shared %s outbox payload: missing project identity", entry.EntityType)
	}
	record, err := s.Durability.ReadSharedRevision(ctx, entry.EntityType, identity.ProjectID, entry.EntityID, entry.Revision)
	if err != nil {
		return err
	}
	if record.MutationKind == "" {
		return nil
	}
	if record.EntityID != entry.EntityID || record.ProjectID != identity.ProjectID || record.Revision != entry.Revision || record.Actor == "" || record.Reason == "" || !json.Valid(record.Payload) {
		return fmt.Errorf("shared %s revision evidence conflicts with outbox", entry.EntityType)
	}
	portable := portableSharedRevision{
		EntityType:    entry.EntityType,
		EntityID:      record.EntityID,
		ProjectID:     record.ProjectID,
		Revision:      record.Revision,
		MutationKind:  record.MutationKind,
		Actor:         record.Actor,
		Reason:        record.Reason,
		ChangedFields: record.ChangedFields,
		Payload:       json.RawMessage(record.Payload),
		RecordedAt:    record.RecordedAt,
	}
	if err := validatePortableRevisionPayload(portable); err != nil {
		return err
	}
	path := s.sharedRevisionPath(record.ProjectID, entry.EntityType, record.EntityID, record.Revision)
	_, err = s.Hub.Transact(ctx, "", "gateway: publish Shared revision evidence", func(worktree string) ([]string, error) {
		var latest portableSharedRevision
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			var latestPayload, expectedPayload bytes.Buffer
			if latestPayloadErr := json.Compact(&latestPayload, latest.Payload); latestPayloadErr != nil {
				return nil, latestPayloadErr
			}
			if expectedPayloadErr := json.Compact(&expectedPayload, portable.Payload); expectedPayloadErr != nil {
				return nil, expectedPayloadErr
			}
			if latest.EntityType != portable.EntityType || latest.EntityID != portable.EntityID || latest.ProjectID != portable.ProjectID || latest.Revision != portable.Revision || latest.MutationKind != portable.MutationKind || latest.Actor != portable.Actor || latest.Reason != portable.Reason || latest.RecordedAt != portable.RecordedAt || !reflect.DeepEqual(latest.ChangedFields, portable.ChangedFields) || !bytes.Equal(latestPayload.Bytes(), expectedPayload.Bytes()) {
				return nil, fmt.Errorf("Hub shared revision evidence conflicts at %s", path)
			}
			return nil, errSharedOutboxNoop
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, portable); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) publishSharedLifecycleEventsOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	if s.Durability == nil {
		return nil
	}
	switch entry.EntityType {
	case "task", "adr", "rule", "milestone", "track":
	default:
		return nil
	}
	var identity struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(entry.Payload, &identity); err != nil || identity.ProjectID == "" {
		return fmt.Errorf("invalid shared %s outbox payload: missing project identity", entry.EntityType)
	}
	events, err := s.Durability.ListSharedLifecycleEvents(ctx, entry.EntityType, identity.ProjectID, entry.EntityID, sqlitestore.SharedLifecycleQueryMaxRows)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	portable := make([]portableSharedLifecycleEvent, 0, len(events))
	for _, event := range events {
		item := portableSharedLifecycleEvent{
			OperationID:   event.OperationID,
			EntityType:    event.EntityType,
			ProjectID:     event.ProjectID,
			EntityID:      event.EntityID,
			Revision:      event.Revision,
			EventKind:     event.EventKind,
			MutationKind:  event.MutationKind,
			FromStatus:    event.FromStatus,
			ToStatus:      event.ToStatus,
			Actor:         event.Actor,
			Reason:        event.Reason,
			Contract:      append(json.RawMessage(nil), event.Contract...),
			ChangedFields: append([]string(nil), event.ChangedFields...),
			RecordedAt:    event.RecordedAt.UTC().Format(time.RFC3339Nano),
		}
		if err := validatePortableLifecycleEvent(item, identity.ProjectID); err != nil {
			return err
		}
		portable = append(portable, item)
	}
	_, err = s.Hub.Transact(ctx, "", "gateway: publish Shared lifecycle evidence", func(worktree string) ([]string, error) {
		changed := make([]string, 0, len(portable))
		for _, event := range portable {
			path := s.sharedLifecycleEventPath(event.ProjectID, event.EntityType, event.EntityID, event.OperationID)
			if path == "../invalid-shared-lifecycle-event" {
				return nil, fmt.Errorf("invalid Shared lifecycle event path")
			}
			var latest portableSharedLifecycleEvent
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if samePortableLifecycleEvent(latest, event) {
					continue
				}
				return nil, fmt.Errorf("Hub Shared lifecycle evidence conflicts at %s", path)
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, event); err != nil {
				return nil, err
			}
			changed = append(changed, path)
		}
		if len(changed) == 0 {
			return nil, errSharedOutboxNoop
		}
		return changed, nil
	})
	return err
}

var sharedOutboxPublishers = map[string]func(*Service, context.Context, sqlitestore.OutboxEntry) error{
	"milestone":             (*Service).publishSharedMilestoneOutbox,
	"track":                 (*Service).publishSharedTrackOutbox,
	"relation":              (*Service).publishSharedRelationOutbox,
	"task":                  (*Service).publishSharedTaskOutbox,
	"adr":                   (*Service).publishSharedADROutbox,
	"rule":                  (*Service).publishSharedRuleOutbox,
	"journal":               (*Service).publishSharedJournalOutbox,
	"project_configuration": (*Service).publishSharedProjectConfigurationOutbox,
}

func (s *Service) publishSharedMilestoneOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var milestone model.Milestone
	if err := json.Unmarshal(entry.Payload, &milestone); err != nil {
		return err
	}
	if err := model.ValidateMilestone(milestone); err != nil {
		return err
	}
	if entry.EntityType != "milestone" || entry.EntityID != milestone.ID || entry.Revision != int64(milestone.Revision) {
		return fmt.Errorf("shared milestone outbox entry identity mismatch")
	}
	path := s.milestonePath(milestone.ProjectID, milestone.ID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared milestone "+milestone.ID, func(worktree string) ([]string, error) {
		var latest model.Milestone
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if latest.ID == milestone.ID && latest.Revision == milestone.Revision && reflect.DeepEqual(latest, milestone) {
				return nil, errSharedOutboxNoop
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, milestone); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) publishSharedTrackOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var track model.Track
	if err := json.Unmarshal(entry.Payload, &track); err != nil {
		return err
	}
	if err := model.ValidateTrack(track); err != nil {
		return err
	}
	if entry.EntityType != "track" || entry.EntityID != track.ID || entry.Revision != int64(track.Revision) {
		return fmt.Errorf("shared Track outbox entry identity mismatch")
	}
	path := s.trackPath(track.ProjectID, track.ID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared Track "+track.ID, func(worktree string) ([]string, error) {
		var latest model.Track
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if err := model.ValidateTrack(latest); err != nil {
				return nil, fmt.Errorf("Hub Track %q is malformed authority: %w", track.ID, err)
			}
			if latest.ID != track.ID || latest.ProjectID != track.ProjectID {
				return nil, fmt.Errorf("Hub Track identity conflicts at %s", path)
			}
			switch {
			case latest.Revision > track.Revision:
				return nil, errSharedOutboxNoop
			case latest.Revision == track.Revision:
				if tracksSemanticallyEqual(latest, track) {
					return nil, errSharedOutboxNoop
				}
				hubDigest, hubDigestErr := trackSemanticDigest(latest)
				outboxDigest, outboxDigestErr := trackSemanticDigest(track)
				if hubDigestErr != nil || outboxDigestErr != nil {
					return nil, fmt.Errorf("Hub Track %q conflicts at revision %d and semantic digests could not be computed", track.ID, track.Revision)
				}
				return nil, fmt.Errorf("Hub Track %q conflicts at revision %d (Hub semantic sha256=%s, Shared outbox semantic sha256=%s)", track.ID, track.Revision, hubDigest, outboxDigest)
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, track); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func tracksSemanticallyEqual(left, right model.Track) bool {
	return reflect.DeepEqual(normalizeTrackTimes(left), normalizeTrackTimes(right))
}

func trackSemanticDigest(track model.Track) (string, error) {
	payload, err := json.Marshal(normalizeTrackTimes(track))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeTrackTimes(track model.Track) model.Track {
	track.CreatedAt = track.CreatedAt.UTC()
	track.UpdatedAt = track.UpdatedAt.UTC()
	if track.CancelledAt != nil {
		cancelledAt := track.CancelledAt.UTC()
		track.CancelledAt = &cancelledAt
	}
	if track.Review != nil {
		review := *track.Review
		review.SubmittedAt = review.SubmittedAt.UTC()
		review.Tasks = append([]model.TrackTaskSnapshot(nil), review.Tasks...)
		track.Review = &review
	}
	return track
}

func (s *Service) publishSharedRelationOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var relation model.Relation
	if err := json.Unmarshal(entry.Payload, &relation); err != nil {
		return err
	}
	if err := model.ValidateRelation(relation); err != nil {
		return err
	}
	if family, err := model.RelationFamilyOf(relation.Source); err != nil || family == model.RelationFamilyPMT {
		return fmt.Errorf("Local-only relation cannot be published to Hub")
	}
	if entry.EntityType != "relation" || entry.EntityID != relation.Identity() || entry.Revision != 1 || entry.Kind != "relation-create" {
		return fmt.Errorf("shared relation outbox entry identity mismatch")
	}
	path := s.relationPath(relation)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared relation", func(worktree string) ([]string, error) {
		var latest model.Relation
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if err := model.ValidateRelation(latest); err != nil {
				return nil, fmt.Errorf("Hub relation is malformed authority: %w", err)
			}
			if latest.Identity() != relation.Identity() || latest.ProjectID != relation.ProjectID {
				return nil, fmt.Errorf("Hub relation identity conflicts at %s", path)
			}
			if reflect.DeepEqual(latest, relation) {
				return nil, errSharedOutboxNoop
			}
			return nil, fmt.Errorf("Hub relation conflicts at %s", path)
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, relation); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) publishSharedTaskOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var task model.TaskAuthoring
	if err := json.Unmarshal(entry.Payload, &task); err != nil {
		return err
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return err
	}
	if entry.EntityType != "task" || entry.EntityID != task.ID || entry.Revision != int64(task.Revision) {
		return fmt.Errorf("shared task outbox entry identity mismatch")
	}
	path := s.taskAuthoringPath(task.ProjectID, task.ID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared task "+task.ID, func(worktree string) ([]string, error) {
		var latest model.TaskAuthoring
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if err := model.ValidateTaskAuthoring(latest); err != nil {
				return nil, fmt.Errorf("Hub task %q is malformed authority: %w", task.ID, err)
			}
			if latest.ID != task.ID || latest.ProjectID != task.ProjectID {
				return nil, fmt.Errorf("Hub task identity conflicts at %s", path)
			}
			switch {
			case latest.Revision > task.Revision:
				return nil, errSharedOutboxNoop
			case latest.Revision < task.Revision:
			case latest.RevisionSHA256 != task.RevisionSHA256:
				return nil, fmt.Errorf("Hub task content digest conflicts at revision %d", task.Revision)
			case latest.UpdatedAt.After(task.UpdatedAt):
				return nil, errSharedOutboxNoop
			case task.UpdatedAt.After(latest.UpdatedAt):
			default:
				latestCanonical, latestErr := json.Marshal(latest)
				taskCanonical, taskErr := json.Marshal(task)
				if latestErr != nil || taskErr != nil {
					return nil, fmt.Errorf("cannot compare Hub task payload at revision %d", task.Revision)
				}
				if bytes.Equal(latestCanonical, taskCanonical) {
					return nil, errSharedOutboxNoop
				}
				return nil, fmt.Errorf("equal-time Hub task payload is contradictory at revision %d", task.Revision)
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, task); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) publishSharedADROutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var adr model.ADR
	if err := json.Unmarshal(entry.Payload, &adr); err != nil {
		return err
	}
	if err := model.ValidateADR(adr); err != nil {
		return err
	}
	path := s.adrPath(adr.ProjectID, adr.ID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared ADR "+adr.ID, func(worktree string) ([]string, error) {
		var latest model.ADR
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if latest.ID == adr.ID && latest.Revision == adr.Revision && reflect.DeepEqual(latest, adr) {
				return nil, errSharedOutboxNoop
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, adr); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) publishSharedRuleOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var rule model.Rule
	if err := json.Unmarshal(entry.Payload, &rule); err != nil {
		return err
	}
	if err := model.ValidateRule(rule); err != nil {
		return err
	}
	if entry.EntityType != "rule" || entry.EntityID != rule.ID || entry.Revision != int64(rule.Revision) {
		return fmt.Errorf("shared rule outbox entry identity mismatch")
	}
	path := s.rulePath(rule.ProjectID, rule.ID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared rule "+rule.ID, func(worktree string) ([]string, error) {
		var latest model.Rule
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if latest.ID == rule.ID && latest.Revision == rule.Revision && reflect.DeepEqual(latest, rule) {
				return nil, errSharedOutboxNoop
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, rule); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) publishSharedProjectConfigurationOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	configuration, err := sqlitestore.DecodeCanonicalProjectConfigurationPayload(entry.Payload)
	if err != nil {
		return err
	}
	if entry.EntityID != configuration.ProjectID || entry.Revision != int64(configuration.Revision) {
		return fmt.Errorf("shared project configuration outbox identity mismatch")
	}
	return s.publishSharedProjectConfiguration(ctx, configuration)
}

func (s *Service) publishSharedJournalOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var journal model.JournalEntry
	if err := json.Unmarshal(entry.Payload, &journal); err != nil {
		return err
	}
	if err := model.ValidateJournalEntry(journal); err != nil {
		return err
	}
	if entry.EntityType != "journal" || entry.EntityID != journal.ID || entry.Revision != 1 {
		return fmt.Errorf("shared journal outbox entry identity mismatch")
	}
	path := s.journalPath(journal.ProjectID, journal.ID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared journal "+journal.ID, func(worktree string) ([]string, error) {
		var latest model.JournalEntry
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if latest.ID == journal.ID && reflect.DeepEqual(latest, journal) {
				return nil, errSharedOutboxNoop
			}
		} else if !IsNotFound(readErr) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, journal); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}
