package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func parseADRSelector(value string, revision int) (string, int, error) {
	if strings.Contains(value, ":") {
		return "", 0, fmt.Errorf("ADR revision selectors must use the separate revision field")
	}
	id := value
	selectorRevision := 0
	if model.ValidateADRIdentifier(id) != nil && model.ValidateCanonicalADRIdentifier(id) != nil {
		return "", 0, fmt.Errorf("invalid ADR identifier")
	}
	if revision != 0 && (selectorRevision != 0 && selectorRevision != revision) {
		return "", 0, fmt.Errorf("ADR revision selector mismatch")
	}
	if revision != 0 {
		selectorRevision = revision
	}
	return id, selectorRevision, nil
}

func (s *Service) ADRReadRevision(ctx context.Context, project, selector string, revision int) (model.ADR, error) {
	id, requestedRevision, err := parseADRSelector(selector, revision)
	if err != nil {
		return model.ADR{}, err
	}
	if requestedRevision == 0 {
		if s.Durability == nil {
			return model.ADR{}, fmt.Errorf("ADR Shared durability is unavailable")
		}
		return s.readSharedADR(ctx, project, id)
	}
	if s.Durability == nil {
		return model.ADR{}, fmt.Errorf("revisioned ADR reads require Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, project); err != nil {
		return model.ADR{}, err
	}
	record, err := s.Durability.ReadSharedRevision(ctx, "adr", project, id, int64(requestedRevision))
	if err != nil {
		return model.ADR{}, err
	}
	var adr model.ADR
	if err := json.Unmarshal(record.Payload, &adr); err != nil {
		return model.ADR{}, fmt.Errorf("decode ADR revision %s:%d: %w", id, requestedRevision, err)
	}
	adr = normalizeADR(adr)
	if record.Revision != int64(requestedRevision) || adr.ID != id || adr.ProjectID != project {
		return model.ADR{}, fmt.Errorf("ADR revision identity mismatch")
	}
	adr.Revision = int(record.Revision)
	adr.RevisionCount = adr.Revision
	if actor := strings.TrimSpace(record.Actor); actor != "" {
		adr.UpdatedBy = actor
	}
	if reason := strings.TrimSpace(record.Reason); reason != "" {
		adr.LastReason = reason
	}
	if recordedAt := parseADRTime(record.RecordedAt); !recordedAt.IsZero() {
		adr.UpdatedAt = recordedAt
	}
	if err := model.ValidateADRRevision(adr, true); err != nil {
		return model.ADR{}, err
	}
	return adr, nil
}

func (s *Service) ADRHistory(ctx context.Context, project, selector string, limit int) (ADRHistoryResult, error) {
	return s.ADRHistoryPage(ctx, project, selector, CollectionPageInput{Limit: limit}, false)
}

func (s *Service) ADRHistoryPage(ctx context.Context, project, selector string, in CollectionPageInput, reverse bool) (ADRHistoryResult, error) {
	id, _, err := parseADRSelector(selector, 0)
	if err != nil {
		return ADRHistoryResult{}, err
	}
	if s.Durability == nil {
		return ADRHistoryResult{}, fmt.Errorf("ADR history requires Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, project); err != nil {
		return ADRHistoryResult{}, err
	}
	kind := "adr-history:" + project + ":" + id
	after := sqlitestore.SharedLifecycleHistoryCursor{}
	if in.Cursor != "" {
		key, decodeErr := pagination.DecodeOpaqueKeyset(in.Cursor, kind)
		if decodeErr != nil {
			return ADRHistoryResult{}, fmt.Errorf("invalid ADR history cursor")
		}
		decoded, decodeErr := sqlitestore.DecodeSharedLifecycleHistoryCursor(key)
		if decodeErr != nil {
			return ADRHistoryResult{}, fmt.Errorf("invalid ADR history cursor")
		}
		after = decoded
	}
	page, err := s.Durability.ListSharedLifecycleHistoryPage(ctx, "adr", project, id, after, sqlitestore.SharedLifecycleQueryMaxRows)
	if err != nil {
		return ADRHistoryResult{}, err
	}
	result := ADRHistoryResult{
		Key:        id,
		ProjectID:  project,
		Items:      make([]model.ADRHistoryEntry, 0, len(page.Records)),
		CursorKind: kind,
	}
	for _, record := range page.Records {
		if record.EntityID != id || record.ProjectID != project {
			return ADRHistoryResult{}, fmt.Errorf("ADR history ownership mismatch")
		}
		result.Items = append(result.Items, model.ADRHistoryEntry{SchemaVersion: model.ADRRevisionSchemaVersion, Key: id, ProjectID: project, Revision: int(record.Revision), MutationKind: record.MutationKind, Actor: record.Actor, Reason: record.Reason, ChangedFields: append([]string(nil), record.ChangedFields...), RecordedAt: parseADRTime(record.RecordedAt)})
	}
	if reverse {
		for left, right := 0, len(result.Items)-1; left < right; left, right = left+1, right-1 {
			result.Items[left], result.Items[right] = result.Items[right], result.Items[left]
		}
	}
	if page.HasMore {
		result.NextCursor = page.NextCursor
		result.HasMore = true
	}
	return result, nil
}

// ADRLegacyRelations is a read-only bridge for persisted legacy supersession
// metadata. It deliberately has no mutation authority and is temporary until
// the owning cleanup task removes the legacy relation surface.
func (s *Service) ADRLegacyRelations(ctx context.Context, project, selector, cursor string) (ADRLegacyRelationsResult, error) {
	if s.Durability == nil {
		return ADRLegacyRelationsResult{}, fmt.Errorf("ADR legacy relations require Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, project); err != nil {
		return ADRLegacyRelationsResult{}, err
	}
	result := ADRLegacyRelationsResult{Items: make([]ADRLegacyRelation, 0)}
	seen := make(map[string]struct{})
	appendRelation := func(adr model.ADR, revision int) {
		adr = normalizeADR(adr)
		if adr.Supersedes == "" {
			return
		}
		key := fmt.Sprintf("%s:%d:%s", adr.ID, revision, adr.Supersedes)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result.Items = append(result.Items, ADRLegacyRelation{
			Key:        adr.ID,
			Revision:   revision,
			Supersedes: adr.Supersedes,
		})
	}
	if selector != "" {
		id, _, err := parseADRSelector(selector, 0)
		if err != nil {
			return ADRLegacyRelationsResult{}, err
		}
		cursorKind := "adr-relations:" + project + ":" + id
		afterRevision := int64(0)
		if cursor != "" {
			key, decodeErr := pagination.DecodeOpaqueKeyset(cursor, cursorKind)
			if decodeErr != nil {
				return ADRLegacyRelationsResult{}, fmt.Errorf("invalid ADR legacy relations cursor")
			}
			afterRevision, err = strconv.ParseInt(key, 10, 64)
			if err != nil || afterRevision < 0 {
				return ADRLegacyRelationsResult{}, fmt.Errorf("invalid ADR legacy relations cursor")
			}
		}
		if afterRevision == 0 {
			entity, readErr := s.Durability.ReadSharedEntity(ctx, "adr", id)
			if readErr != nil {
				return ADRLegacyRelationsResult{}, readErr
			}
			var current model.ADR
			if err := json.Unmarshal(entity.Payload, &current); err != nil {
				return ADRLegacyRelationsResult{}, err
			}
			if current.ProjectID != project || current.ID != id {
				return ADRLegacyRelationsResult{}, fmt.Errorf("ADR legacy relation ownership mismatch")
			}
			current = normalizeADR(current)
			appendRelation(current, current.Revision)
		}
		history, err := s.Durability.ListSharedHistoryPage(ctx, "adr", project, id, afterRevision, sqlitestore.SharedLifecycleQueryMaxRows)
		if err != nil {
			return ADRLegacyRelationsResult{}, err
		}
		for _, record := range history.Records {
			var historical model.ADR
			if err := json.Unmarshal(record.Payload, &historical); err != nil {
				return ADRLegacyRelationsResult{}, err
			}
			appendRelation(historical, int(record.Revision))
		}
		if history.HasMore {
			result.NextCursor = pagination.EncodeServerCursor(cursorKind, strconv.FormatInt(history.NextRevision, 10))
			result.HasMore = true
		}
		return result, nil
	}
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{EntityType: "adr", ProjectID: project, IncludeArchived: true, Limit: sqlitestore.SharedLifecycleQueryMaxRows, Cursor: cursor})
	if err != nil {
		return ADRLegacyRelationsResult{}, err
	}
	for _, entity := range page.Entities {
		var current model.ADR
		if err := json.Unmarshal(entity.Payload, &current); err != nil {
			return ADRLegacyRelationsResult{}, err
		}
		current = normalizeADR(current)
		appendRelation(current, current.Revision)
		history, err := s.Durability.ListSharedHistory(ctx, "adr", project, entity.ID, sqlitestore.SharedLifecycleQueryMaxRows)
		if err != nil {
			return ADRLegacyRelationsResult{}, err
		}
		for _, record := range history {
			var historical model.ADR
			if err := json.Unmarshal(record.Payload, &historical); err != nil {
				return ADRLegacyRelationsResult{}, err
			}
			appendRelation(historical, int(record.Revision))
		}
	}
	result.NextCursor = page.NextCursor
	result.HasMore = page.HasMore
	return result, nil
}

func (s *Service) ADRQuery(ctx context.Context, project string, in ADRQueryInput) (ADRListPageResult, error) {
	if err := validateEntityProject(project); err != nil {
		return ADRListPageResult{}, err
	}
	if s.Durability == nil {
		return ADRListPageResult{}, fmt.Errorf("ADR Shared durability is unavailable")
	}
	text := strings.ToLower(strings.TrimSpace(in.Text))
	status := strings.TrimSpace(in.Status)
	if status != "" && status != model.ADRStatusProposed && status != model.ADRStatusAccepted && status != model.ADRStatusSuperseded && status != model.ADRStatusArchived {
		return ADRListPageResult{}, fmt.Errorf("invalid ADR status filter")
	}
	page, err := s.querySharedADRs(ctx, project, text, status, in.IncludeArchived, sqlitestore.SharedLifecycleQueryMaxRows, in.Cursor)
	if err != nil {
		return ADRListPageResult{}, err
	}
	return ADRListPageResult{
		ADRs:       page.ADRs,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
	}, nil
}

func parseADRTime(value string) (resultTime time.Time) {
	resultTime, _ = time.Parse(time.RFC3339Nano, value)
	return resultTime
}

func changedADRFields(in ADRUpdateInput, previous model.ADR) []string {
	fields := make([]string, 0, 6)
	if in.Title != nil && *in.Title != previous.Title {
		fields = append(fields, "title")
	}
	if in.Summary != nil && *in.Summary != previous.Summary {
		fields = append(fields, "summary")
	}
	if in.Context != nil && *in.Context != previous.Context {
		fields = append(fields, "context")
	}
	if in.Decision != nil && *in.Decision != previous.Decision {
		fields = append(fields, "decision")
	}
	if in.Consequences != nil && *in.Consequences != previous.Consequences {
		fields = append(fields, "consequences")
	}
	if in.Status != nil && *in.Status != previous.Status {
		fields = append(fields, "status")
	}
	return fields
}
