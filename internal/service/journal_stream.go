package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type JournalContractInput struct {
	Stream string `json:"stream"`
}

type JournalAddInput struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

type JournalReadInput struct {
	Key string `json:"key"`
}

type JournalListInput struct {
	Stream string `json:"stream,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type JournalListResult struct {
	Items      []model.JournalEntry `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
	HasMore    bool                 `json:"has_more,omitempty"`
	CursorKind string               `json:"-"`
}

// journalViolationsError carries the bounded structured violations the
// contract requires: every failing path is reported and nothing is written.
type journalViolationsError struct {
	violations []model.JournalFieldViolation
}

func (e journalViolationsError) Error() string {
	parts := make([]string, 0, len(e.violations))
	for _, violation := range e.violations {
		parts = append(parts, violation.Error())
	}
	return "journal data violates the stream contract: " + strings.Join(parts, "; ")
}

// StructuredActionError reports the bounded per-path violations through the
// canonical structured-error surface.
func (e journalViolationsError) StructuredActionError() map[string]any {
	items := make([]any, 0, len(e.violations))
	for _, violation := range e.violations {
		items = append(items, map[string]any{"path": violation.Path, "message": violation.Message})
	}
	return map[string]any{"code": "JOURNAL_CONTRACT_VIOLATION", "message": e.Error(), "violations": items}
}

// JournalViolations exposes the structured violations on a journal/add
// contract failure for boundary projection.
func JournalViolations(err error) []model.JournalFieldViolation {
	var target journalViolationsError
	if errors.As(err, &target) {
		return target.violations
	}
	return nil
}

// journalSession resolves the authenticated durable Session for journal
// provenance and writer authority. Fail closed without a bound session.
func (s *Service) journalSession(ctx context.Context) (durableSession.Record, error) {
	if s.Durability == nil {
		return durableSession.Record{}, fmt.Errorf("journal Shared durability is unavailable")
	}
	sessionID := AgentSessionID(ctx)
	if sessionID == "" {
		return durableSession.Record{}, fmt.Errorf("journal requires an authenticated durable Session")
	}
	record, err := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID)
	if err != nil {
		return durableSession.Record{}, fmt.Errorf("journal session authority is unavailable: %w", err)
	}
	if record.Status != durableSession.StatusActive || !durableSession.IsWorkflowRole(record.Role) || record.ProjectID == "" || record.ProjectCode == "" {
		return durableSession.Record{}, fmt.Errorf("journal session is not an active project-bound workflow Session")
	}
	return record, nil
}

// JournalContract returns the published ADR56 rev2 contract for one stream.
func (s *Service) JournalContract(ctx context.Context, in JournalContractInput) (model.JournalStreamContract, error) {
	if _, err := s.journalSession(ctx); err != nil {
		return model.JournalStreamContract{}, err
	}
	return model.JournalStreamContractFor(in.Stream)
}

// JournalAdd appends one contract-validated immutable journal entry through
// the canonical SharedLifecycle create unit. The JRN sequence, project,
// actor/role/session provenance, and timestamps are server-owned.
func (s *Service) JournalAdd(ctx context.Context, in JournalAddInput) (model.JournalEntry, OperationResult, error) {
	session, err := s.journalSession(ctx)
	if err != nil {
		return model.JournalEntry{}, OperationResult{}, err
	}
	stream := model.JournalStream(strings.TrimSpace(in.Stream))
	if !model.JournalStreamWriterAllowed(stream, session.Role) {
		return model.JournalEntry{}, OperationResult{}, fmt.Errorf("Session role %q is not a writer for journal stream %q", session.Role, stream)
	}
	if violations := model.ValidateJournalStreamData(stream, in.Data); len(violations) > 0 {
		return model.JournalEntry{}, OperationResult{}, journalViolationsError{violations: violations}
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		encoded, err := json.Marshal(struct {
			Stream    string          `json:"stream"`
			Data      json.RawMessage `json:"data"`
			SessionID string          `json:"session_id"`
		}{
			Stream:    string(stream),
			Data:      in.Data,
			SessionID: session.ID,
		})
		if err != nil {
			return model.JournalEntry{}, OperationResult{}, err
		}
		digest := sha256.Sum256(encoded)
		operationID = "journal-shared-" + hex.EncodeToString(digest[:])
	}
	created := s.durableNow()
	var entry model.JournalEntry
	_, id, payload, err := s.Durability.CommitSharedLifecycleCreate(ctx, sqlitestore.SharedLifecycleCreate{
		OperationID:         operationID,
		EntityType:          "journal",
		ProjectID:           session.ProjectID,
		ProjectCode:         session.ProjectCode,
		InitialNextNumber:   1,
		Kind:                "journal-add",
		HistoryMutationKind: "create",
		Actor:               session.ID,
		Reason:              "journal add",
		ChangedFields:       []string{"stream", "data"},
		CreatedAt:           created,
		BuildPayload: func(entityID string) ([]byte, error) {
			_, number, parseErr := model.ParseJournalID(entityID)
			if parseErr != nil {
				return nil, parseErr
			}
			actor := session.ID
			if session.SessionRef != nil && *session.SessionRef != "" {
				actor = *session.SessionRef
			}
			entry = model.JournalEntry{
				SchemaVersion: model.SchemaVersion,
				ID:            entityID,
				ProjectID:     session.ProjectID,
				Status:        model.JournalStatusPublished,
				Stream:        stream,
				Data:          append(json.RawMessage(nil), in.Data...),
				Actor:         actor,
				Role:          session.Role,
				SessionID:     session.ID,
				Sequence:      number,
				CreatedAt:     created,
			}
			payload, marshalErr := json.Marshal(entry)
			if marshalErr != nil {
				return nil, marshalErr
			}
			if err := model.ValidateJournalEntry(entry); err != nil {
				return nil, err
			}
			return payload, nil
		},
	})
	if err != nil {
		return model.JournalEntry{}, OperationResult{}, err
	}
	if err := json.Unmarshal(payload, &entry); err != nil {
		return model.JournalEntry{}, OperationResult{}, fmt.Errorf("decode committed journal %s: %w", id, err)
	}
	return entry, OperationResult{
		OperationID: operationID,
		ProjectID:   session.ProjectID,
		EntityKey:   id,
		Revision:    1,
		Status:      "recorded",
	}, nil
}

func (s *Service) readSharedJournalEntry(ctx context.Context, key string) (model.JournalEntry, error) {
	if err := model.ValidateJournalID(key); err != nil {
		return model.JournalEntry{}, fmt.Errorf("invalid journal key")
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "journal", key)
	if err != nil {
		return model.JournalEntry{}, err
	}
	var entry model.JournalEntry
	if err := json.Unmarshal(entity.Payload, &entry); err != nil {
		return model.JournalEntry{}, fmt.Errorf("decode shared journal %s: %w", key, err)
	}
	if entry.ID != key {
		return model.JournalEntry{}, fmt.Errorf("shared journal identity mismatch")
	}
	if err := model.ValidateJournalEntry(entry); err != nil {
		return model.JournalEntry{}, err
	}
	return entry, nil
}

// JournalRead returns one canonical journal entry by its JRN key, scoped to
// the authenticated session's project.
func (s *Service) JournalRead(ctx context.Context, in JournalReadInput) (model.JournalEntry, error) {
	session, err := s.journalSession(ctx)
	if err != nil {
		return model.JournalEntry{}, err
	}
	entry, err := s.readSharedJournalEntry(ctx, in.Key)
	if err != nil {
		return model.JournalEntry{}, err
	}
	if entry.ProjectID != session.ProjectID {
		return model.JournalEntry{}, fmt.Errorf("journal %q is outside the session project", in.Key)
	}
	return entry, nil
}

// JournalList returns the bounded keyset-ordered journal page for the
// authenticated session's project, optionally filtered by stream.
func (s *Service) JournalList(ctx context.Context, in JournalListInput) (JournalListResult, error) {
	session, err := s.journalSession(ctx)
	if err != nil {
		return JournalListResult{}, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 64
	}
	if limit < 1 || limit > 256 {
		return JournalListResult{}, fmt.Errorf("journal list limit must be 1-256")
	}
	filters := map[string]string{}
	if strings.TrimSpace(in.Stream) != "" {
		stream := model.JournalStream(strings.TrimSpace(in.Stream))
		if _, err := model.JournalStreamContractFor(string(stream)); err != nil {
			return JournalListResult{}, err
		}
		filters["stream"] = string(stream)
	}
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{
		EntityType: "journal", ProjectID: session.ProjectID, Filters: filters,
		IncludeArchived: true, Limit: limit, Cursor: in.Cursor,
	})
	if err != nil {
		return JournalListResult{}, err
	}
	items := make([]model.JournalEntry, 0, len(page.Entities))
	for _, entity := range page.Entities {
		var entry model.JournalEntry
		if err := json.Unmarshal(entity.Payload, &entry); err != nil {
			return JournalListResult{}, fmt.Errorf("decode shared journal %s: %w", entity.ID, err)
		}
		if entry.ID != entity.ID || entry.ProjectID != session.ProjectID {
			return JournalListResult{}, fmt.Errorf("shared journal identity mismatch")
		}
		if err := model.ValidateJournalEntry(entry); err != nil {
			return JournalListResult{}, err
		}
		items = append(items, entry)
	}
	return JournalListResult{
		Items:      items,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
	}, nil
}

// journalPath is the canonical Hub projection path for one journal entry.
func (s *Service) journalPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateJournalID(id) != nil {
		return "../invalid-journal-id"
	}
	return s.projectPrefix(project) + "/journals/" + id + ".json"
}
