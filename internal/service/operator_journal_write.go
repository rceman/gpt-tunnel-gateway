package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

var operatorFallbackSequence atomic.Uint64

func allowedBootstrapOperatorKind(kind model.OperatorJournalKind) bool {
	switch kind {
	case model.OperatorUserTalk, model.OperatorReasoningSummary, model.OperatorTaskPlan, model.OperatorTaskReview, model.OperatorCorrection:
		return true
	default:
		return false
	}
}

func validateOperatorInput(projectID string, sessionID *string, kind model.OperatorJournalKind, summary string, content model.OperatorJournalContent, references model.OperatorJournalReferences, supersedes, actor string, allowCheckpoint bool) error {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	if !allowedBootstrapOperatorKind(kind) && !(allowCheckpoint && kind == model.OperatorCheckpoint) {
		return fmt.Errorf("operator_record kind %q is reserved or invalid", kind)
	}
	now := time.Now().UTC()
	probe := model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: "AAA-OPR1", ProjectID: projectID, SessionID: sessionID, Kind: kind, Summary: summary, Content: content, References: references, SupersedesEventID: supersedes, Actor: actor, OccurredAt: now, RecordedAt: now}
	return model.ValidateOperatorJournalEvent(probe)
}

func parseOperatorOccurredAt(value *string) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if strings.TrimSpace(*value) != *value || *value == "" {
		return time.Time{}, fmt.Errorf("occurred_at must be a strict RFC3339 timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return time.Time{}, fmt.Errorf("occurred_at must be a strict RFC3339 timestamp")
	}
	return parsed.UTC(), nil
}

func (s *Service) operatorRecord(ctx context.Context, in OperatorRecordInput, allowCheckpoint bool) (model.OperatorJournalEvent, OperationResult, error) {
	if err := validateOperatorInput(in.ProjectID, in.SessionID, in.Kind, in.Summary, in.Content, in.References, in.SupersedesEventID, in.Actor, allowCheckpoint); err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	occurredAt, err := parseOperatorOccurredAt(in.OccurredAt)
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	if err := validateOperationalOperatorReferences(in.References, identifiers.ProjectCode); err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	if in.SupersedesEventID != "" {
		if err := s.validateSharedJournalSupersession(ctx, in.ProjectID, identifiers.ProjectCode, in.SupersedesEventID); err != nil {
			return model.OperatorJournalEvent{}, OperationResult{}, err
		}
	}
	if s.Durability == nil {
		return model.OperatorJournalEvent{}, OperationResult{}, fmt.Errorf("Shared operator journal authority is unavailable")
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		encoded, marshalErr := json.Marshal(struct {
			ProjectID  string                          `json:"project_id"`
			SessionID  *string                         `json:"session_id"`
			Kind       model.OperatorJournalKind       `json:"kind"`
			Summary    string                          `json:"summary"`
			Content    model.OperatorJournalContent    `json:"content"`
			References model.OperatorJournalReferences `json:"references"`
			Supersedes string                          `json:"supersedes_event_id"`
			Actor      string                          `json:"actor"`
			OccurredAt *string                         `json:"occurred_at"`
		}{in.ProjectID, in.SessionID, in.Kind, in.Summary, in.Content, in.References, in.SupersedesEventID, in.Actor, in.OccurredAt})
		if marshalErr != nil {
			return model.OperatorJournalEvent{}, OperationResult{}, marshalErr
		}
		digest := sha256.Sum256(encoded)
		operationID = "journal-shared-" + hex.EncodeToString(digest[:]) + fmt.Sprintf("-%d-%d", time.Now().UnixNano(), operatorFallbackSequence.Add(1))
	}
	var event model.OperatorJournalEvent
	_, _, payload, err := s.Durability.CommitSharedJournalCreate(ctx, sqlitestore.SharedJournalCreate{
		OperationID: operationID, ProjectID: in.ProjectID, ProjectCode: identifiers.ProjectCode,
		InitialNextEventNumber: 1, SupersedesEventID: in.SupersedesEventID, Kind: "operator-journal-record", CreatedAt: time.Now().UTC(),
		BuildPayload: func(eventID string) ([]byte, error) {
			recordedAt := time.Now().UTC()
			if occurredAt.IsZero() {
				occurredAt = recordedAt
			}
			if occurredAt.After(recordedAt) {
				return nil, fmt.Errorf("occurred_at cannot be after recorded_at")
			}
			event = model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: eventID, ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: in.Kind, Summary: in.Summary, Content: in.Content, References: in.References, SupersedesEventID: in.SupersedesEventID, Actor: in.Actor, OccurredAt: occurredAt, RecordedAt: recordedAt}
			if err := model.ValidateOperatorJournalEvent(event); err != nil {
				return nil, err
			}
			if err := validateOperationalOperatorReferences(event.References, identifiers.ProjectCode); err != nil {
				return nil, err
			}
			return json.Marshal(event)
		},
	})
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	if len(payload) == 0 {
		return model.OperatorJournalEvent{}, OperationResult{}, fmt.Errorf("Shared operator journal payload is empty")
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	return event, OperationResult{OperationID: operationID, ProjectID: in.ProjectID, Status: "recorded"}, nil
}

func (s *Service) OperatorRecord(ctx context.Context, in OperatorRecordInput) (model.OperatorJournalEvent, OperationResult, error) {
	return s.operatorRecord(ctx, in, false)
}

func (s *Service) OperatorCheckpoint(ctx context.Context, in OperatorCheckpointInput) (model.OperatorJournalEvent, OperationResult, error) {
	event, operation, err := s.operatorRecord(ctx, OperatorRecordInput{
		ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: model.OperatorCheckpoint,
		Summary: in.Summary, Content: in.Content, References: in.References,
		OccurredAt: in.OccurredAt, Actor: in.Actor, WriteOptions: in.WriteOptions,
	}, true)
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	operation.Status = "checkpointed"
	return event, operation, nil
}
