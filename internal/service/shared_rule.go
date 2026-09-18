package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// ruleEffectiveMaxRules bounds the accepted named-rule projection that forms
// the effective set a Session acknowledges.
const ruleEffectiveMaxRules = 256

func (s *Service) readSharedRule(ctx context.Context, projectID, id string) (model.Rule, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.Rule{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "rule", id)
	if err != nil {
		return model.Rule{}, err
	}
	var rule model.Rule
	if err := json.Unmarshal(entity.Payload, &rule); err != nil {
		return model.Rule{}, fmt.Errorf("decode shared rule %s: %w", id, err)
	}
	if rule.ID != id || rule.ProjectID != projectID {
		return model.Rule{}, fmt.Errorf("shared rule ownership mismatch")
	}
	rule = normalizeRule(rule)
	if err := model.ValidateRule(rule); err != nil {
		return model.Rule{}, err
	}
	return rule, nil
}

func normalizeRule(rule model.Rule) model.Rule {
	if rule.Status == "" {
		rule.Status = model.RuleStatusProposed
	}
	if rule.Revision == 0 {
		rule.Revision = 1
	}
	if rule.CreatedBy == "" {
		rule.CreatedBy = "migration"
	}
	if rule.UpdatedBy == "" {
		rule.UpdatedBy = rule.CreatedBy
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = rule.CreatedAt
	}
	if rule.LastReason == "" {
		rule.LastReason = "init"
	}
	if rule.Status == model.RuleStatusArchived {
		if rule.ArchivedAt == nil && !rule.UpdatedAt.IsZero() {
			at := rule.UpdatedAt
			rule.ArchivedAt = &at
		}
		if rule.ArchivedBy == "" {
			rule.ArchivedBy = rule.UpdatedBy
		}
		if rule.ArchiveReason == "" {
			rule.ArchiveReason = rule.LastReason
		}
	}
	return rule
}

type sharedRulePage struct {
	Rules      []model.Rule
	NextCursor string
	HasMore    bool
	CursorKind string
}

func (s *Service) querySharedRules(ctx context.Context, projectID, text, status string, includeArchived bool, limit int, cursor string) (sharedRulePage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return sharedRulePage{}, err
	}
	filters := map[string]string{}
	if status != "" {
		filters["status"] = status
	}
	includeArchived = includeArchived || status == model.RuleStatusArchived
	entities, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{
		EntityType: "rule", ProjectID: projectID, Text: text, Filters: filters,
		IncludeArchived: includeArchived, Limit: limit, Cursor: cursor,
	})
	if err != nil {
		return sharedRulePage{}, err
	}
	items := make([]model.Rule, 0, len(entities.Entities))
	for _, entity := range entities.Entities {
		var rule model.Rule
		if err := json.Unmarshal(entity.Payload, &rule); err != nil {
			return sharedRulePage{}, fmt.Errorf("decode shared rule %s: %w", entity.ID, err)
		}
		if rule.ProjectID != projectID || rule.ID != entity.ID {
			return sharedRulePage{}, fmt.Errorf("shared rule identity mismatch")
		}
		rule = normalizeRule(rule)
		if err := model.ValidateRule(rule); err != nil {
			return sharedRulePage{}, err
		}
		items = append(items, rule)
	}
	return sharedRulePage{
		Rules:      items,
		NextCursor: entities.NextCursor,
		HasMore:    entities.HasMore,
		CursorKind: entities.CursorKind,
	}, nil
}

func parseRuleSelector(value string, revision int) (string, int, error) {
	if strings.Contains(value, ":") {
		return "", 0, fmt.Errorf("rule revision selectors must use the separate revision field")
	}
	if err := model.ValidateRuleID(value); err != nil {
		return "", 0, fmt.Errorf("invalid rule identifier")
	}
	return value, revision, nil
}

func (s *Service) ruleRevisionOperationID(kind string, input any) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(kind+":"), raw...))
	return "rule-" + hex.EncodeToString(digest[:]), nil
}

func (s *Service) RuleCreate(ctx context.Context, in RuleCreateInput) (OperationResult, error) {
	if s.Durability == nil {
		return OperationResult{}, fmt.Errorf("rule Shared durability is unavailable")
	}
	return s.ruleCreateShared(ctx, in)
}

func (s *Service) ruleCreateShared(ctx context.Context, in RuleCreateInput) (OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.Rule.ProjectID); err != nil {
		return OperationResult{}, err
	}
	project, err := s.EffectiveProjectConfig(in.Rule.ProjectID)
	if err != nil || model.ValidateProjectCode(project.ProjectCode) != nil {
		return OperationResult{}, fmt.Errorf("project %q has no local project code", in.Rule.ProjectID)
	}
	extra, err := s.relationCreateSugar(ctx, in.Rule.ProjectID, model.RelationFamilyRule, in.RelationType, in.RelationTarget, in.Rule.CreatedBy)
	if err != nil {
		return OperationResult{}, err
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		encoded, err := json.Marshal(in)
		if err != nil {
			return OperationResult{}, err
		}
		digest := sha256.Sum256(encoded)
		operationID = "rule-shared-" + hex.EncodeToString(digest[:])
	}
	var created model.Rule
	_, id, _, err := s.Durability.CommitSharedLifecycleCreate(ctx, sqlitestore.SharedLifecycleCreate{
		OperationID:         operationID,
		EntityType:          "rule",
		ProjectID:           in.Rule.ProjectID,
		ProjectCode:         project.ProjectCode,
		InitialNextNumber:   1,
		Kind:                "rule-create",
		HistoryMutationKind: "create",
		Actor:               firstNonEmpty(in.Rule.CreatedBy, "server"),
		Reason:              "create",
		ChangedFields:       []string{"title", "summary", "name", "value", "description"},
		CreatedAt:           time.Now().UTC(),
		BuildPayload: func(ruleID string) ([]byte, error) {
			created = in.Rule
			created.SchemaVersion = model.SchemaVersion
			created.ID = ruleID
			created.CreatedAt = time.Now().UTC()
			created.Revision = 1
			created.UpdatedAt = time.Time{}
			created.LastReason = "create"
			payload, err := json.Marshal(created)
			if err != nil {
				return nil, err
			}
			payload, err = sqlitestore.ApplySharedLifecycleCreateDefaults("rule", payload)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(payload, &created); err != nil {
				return nil, err
			}
			if err := model.ValidateRule(created); err != nil {
				return nil, err
			}
			return payload, nil
		},
		ExtraStatements: extra,
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

func (s *Service) RuleRead(ctx context.Context, project, id string) (model.Rule, error) {
	return s.RuleReadRevision(ctx, project, id, 0)
}

func (s *Service) RuleReadRevision(ctx context.Context, project, selector string, revision int) (model.Rule, error) {
	id, requestedRevision, err := parseRuleSelector(selector, revision)
	if err != nil {
		return model.Rule{}, err
	}
	if requestedRevision == 0 {
		if s.Durability == nil {
			return model.Rule{}, fmt.Errorf("rule Shared durability is unavailable")
		}
		return s.readSharedRule(ctx, project, id)
	}
	if s.Durability == nil {
		return model.Rule{}, fmt.Errorf("revisioned rule reads require Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, project); err != nil {
		return model.Rule{}, err
	}
	record, err := s.Durability.ReadSharedRevision(ctx, "rule", project, id, int64(requestedRevision))
	if err != nil {
		return model.Rule{}, err
	}
	var rule model.Rule
	if err := json.Unmarshal(record.Payload, &rule); err != nil {
		return model.Rule{}, fmt.Errorf("decode rule revision %s:%d: %w", id, requestedRevision, err)
	}
	rule = normalizeRule(rule)
	if record.Revision != int64(requestedRevision) || rule.ID != id || rule.ProjectID != project {
		return model.Rule{}, fmt.Errorf("rule revision identity mismatch")
	}
	rule.Revision = int(record.Revision)
	if actor := strings.TrimSpace(record.Actor); actor != "" {
		rule.UpdatedBy = actor
	}
	if reason := strings.TrimSpace(record.Reason); reason != "" {
		rule.LastReason = reason
	}
	if recordedAt := parseADRTime(record.RecordedAt); !recordedAt.IsZero() {
		rule.UpdatedAt = recordedAt
	}
	if err := model.ValidateRule(rule); err != nil {
		return model.Rule{}, err
	}
	return rule, nil
}

func (s *Service) RuleUpdateCurrent(ctx context.Context, in RuleUpdateInput) (OperationResult, error) {
	current, err := s.readSharedRule(ctx, in.ProjectID, in.RuleID)
	if err != nil {
		return OperationResult{}, err
	}
	in.ExpectedRevision = current.Revision
	return s.RuleUpdate(ctx, in)
}

func (s *Service) RuleUpdate(ctx context.Context, in RuleUpdateInput) (OperationResult, error) {
	if s.Durability == nil {
		return OperationResult{}, fmt.Errorf("rule update requires Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ExpectedRevision < 1 || !validRuleMutationReason(in.Reason) || strings.TrimSpace(in.UpdatedBy) == "" {
		return OperationResult{}, fmt.Errorf("rule update requires expected_revision, reason, and updated_by")
	}
	if in.Title == nil && in.Summary == nil && in.Value == nil && in.Description == nil && in.Status == nil {
		return OperationResult{}, fmt.Errorf("rule update requires at least one mutable content field")
	}
	id, _, err := parseRuleSelector(in.RuleID, 0)
	if err != nil {
		return OperationResult{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "rule", id)
	if err != nil {
		return OperationResult{}, err
	}
	var current model.Rule
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		return OperationResult{}, err
	}
	current = normalizeRule(current)
	previous := current
	if current.ProjectID != in.ProjectID || current.ID != id {
		return OperationResult{}, fmt.Errorf("rule ownership mismatch")
	}
	if err := model.ValidateRule(previous); err != nil {
		return OperationResult{}, err
	}
	if current.Revision != in.ExpectedRevision {
		return OperationResult{}, fmt.Errorf("rule revision conflict expected=%d actual=%d", in.ExpectedRevision, current.Revision)
	}
	if current.Status == model.RuleStatusArchived {
		return OperationResult{}, fmt.Errorf("archived rule cannot be updated")
	}
	contentChanged := false
	if in.Title != nil && *in.Title != previous.Title {
		contentChanged = true
		current.Title = *in.Title
	}
	if in.Summary != nil && *in.Summary != previous.Summary {
		contentChanged = true
		current.Summary = *in.Summary
	}
	if in.Value != nil && !bytes.Equal(*in.Value, previous.Value) {
		contentChanged = true
		current.Value = *in.Value
	}
	if in.Description != nil && *in.Description != previous.Description {
		contentChanged = true
		current.Description = *in.Description
	}
	if in.Status != nil && *in.Status != previous.Status {
		current.Status = *in.Status
	}
	statusChanged := current.Status != previous.Status
	if !contentChanged && !statusChanged {
		opID := durableMutationOperationID(ctx)
		if opID == "" {
			opID, err = s.ruleRevisionOperationID("update", in)
			if err != nil {
				return OperationResult{}, err
			}
		}
		return OperationResult{
			OperationID: opID,
			ProjectID:   in.ProjectID,
			EntityKey:   id,
			Revision:    previous.Revision,
			Status:      "unchanged",
			Hub:         hub.TransactionResult{Paths: []string{}},
		}, nil
	}
	if statusChanged {
		if contentChanged {
			current.Revision++
		}
		current.UpdatedBy = strings.TrimSpace(in.UpdatedBy)
		current.UpdatedAt = s.durableNow()
		current.LastReason = strings.TrimSpace(in.Reason)
		if current.Status == model.RuleStatusArchived && current.ArchivedAt == nil {
			at := current.UpdatedAt
			current.ArchivedAt = &at
			current.ArchivedBy = current.UpdatedBy
			current.ArchiveReason = current.LastReason
		}
		if err := model.ValidateRule(current); err != nil {
			return OperationResult{}, err
		}
		payload, err := json.Marshal(current)
		if err != nil {
			return OperationResult{}, err
		}
		opID := durableMutationOperationID(ctx)
		if opID == "" {
			opID, err = s.ruleRevisionOperationID("update", in)
			if err != nil {
				return OperationResult{}, err
			}
		}
		contract, err := ruleLifecycleContract(previous, current, entity.Payload, payload, in.ExpectedRevision)
		if err != nil {
			return OperationResult{}, err
		}
		if _, err := s.Durability.CommitSharedLifecycleEvent(ctx, sqlitestore.SharedLifecycleEventRequest{
			OperationID:           opID,
			EntityType:            "rule",
			ProjectID:             in.ProjectID,
			EntityID:              id,
			ExpectedRevision:      int64(in.ExpectedRevision),
			ExpectedStoreRevision: entity.Revision,
			ExpectedPayload:       entity.Payload,
			Revision:              int64(current.Revision),
			Kind:                  "rule-update",
			EventKind:             sqlitestore.SharedLifecycleEventKindStatus,
			HistoryMutationKind:   "status",
			FromStatus:            previous.Status,
			ToStatus:              current.Status,
			Payload:               payload,
			Actor:                 current.UpdatedBy,
			Reason:                current.LastReason,
			ChangedFields:         changedRuleFields(in, previous),
			Contract:              contract,
			CreatedAt:             current.UpdatedAt,
		}); err != nil {
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
	current.Revision++
	current.UpdatedBy = strings.TrimSpace(in.UpdatedBy)
	current.UpdatedAt = s.durableNow()
	current.LastReason = strings.TrimSpace(in.Reason)
	if err := model.ValidateRule(current); err != nil {
		return OperationResult{}, err
	}
	payload, err := json.Marshal(current)
	if err != nil {
		return OperationResult{}, err
	}
	opID := durableMutationOperationID(ctx)
	if opID == "" {
		opID, err = s.ruleRevisionOperationID("update", in)
		if err != nil {
			return OperationResult{}, err
		}
	}
	if _, err := s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{OperationID: opID, EntityType: "rule", ProjectID: in.ProjectID, EntityID: id, ExpectedRevision: int64(in.ExpectedRevision), ExpectedStoreRevision: entity.Revision, Revision: int64(current.Revision), Kind: "rule-update", HistoryMutationKind: "update", Payload: payload, Actor: current.UpdatedBy, Reason: current.LastReason, ChangedFields: changedRuleFields(in, previous), CreatedAt: current.UpdatedAt}); err != nil {
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

func ruleLifecycleContract(previous, current model.Rule, previousPayload, payload []byte, expectedRevision int) ([]byte, error) {
	previousDigest := sha256.Sum256(previousPayload)
	payloadDigest := sha256.Sum256(payload)
	return json.Marshal(struct {
		ExpectedRevision int    `json:"expected_revision"`
		Revision         int    `json:"revision"`
		FromStatus       string `json:"from_status"`
		ToStatus         string `json:"to_status"`
		PreviousSHA256   string `json:"previous_sha256"`
		PayloadSHA256    string `json:"payload_sha256"`
	}{expectedRevision, current.Revision, previous.Status, current.Status, hex.EncodeToString(previousDigest[:]), hex.EncodeToString(payloadDigest[:])})
}

func changedRuleFields(in RuleUpdateInput, previous model.Rule) []string {
	fields := make([]string, 0, 5)
	if in.Title != nil && *in.Title != previous.Title {
		fields = append(fields, "title")
	}
	if in.Summary != nil && *in.Summary != previous.Summary {
		fields = append(fields, "summary")
	}
	if in.Value != nil && !bytes.Equal(*in.Value, previous.Value) {
		fields = append(fields, "value")
	}
	if in.Description != nil && *in.Description != previous.Description {
		fields = append(fields, "description")
	}
	if in.Status != nil && *in.Status != previous.Status {
		fields = append(fields, "status")
	}
	return fields
}

func validRuleMutationReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && len([]byte(reason)) <= 1024 && !strings.ContainsAny(reason, "\x00\r\n")
}

func (s *Service) RuleArchiveCurrent(ctx context.Context, in RuleArchiveInput) (OperationResult, error) {
	current, err := s.readSharedRule(ctx, in.ProjectID, in.RuleID)
	if err != nil {
		return OperationResult{}, err
	}
	in.ExpectedRevision = current.Revision
	return s.RuleArchive(ctx, in)
}

func (s *Service) RuleArchive(ctx context.Context, in RuleArchiveInput) (OperationResult, error) {
	if s.Durability == nil {
		return OperationResult{}, fmt.Errorf("rule archive requires Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ExpectedRevision < 1 || !validRuleMutationReason(in.Reason) || strings.TrimSpace(in.ArchivedBy) == "" {
		return OperationResult{}, fmt.Errorf("rule archive requires expected_revision, reason, and archived_by")
	}
	id, _, err := parseRuleSelector(in.RuleID, 0)
	if err != nil {
		return OperationResult{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "rule", id)
	if err != nil {
		return OperationResult{}, err
	}
	var current model.Rule
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		return OperationResult{}, err
	}
	current = normalizeRule(current)
	previous := current
	if current.ProjectID != in.ProjectID || current.ID != id {
		return OperationResult{}, fmt.Errorf("rule ownership mismatch")
	}
	if current.Revision != in.ExpectedRevision {
		return OperationResult{}, fmt.Errorf("rule revision conflict expected=%d actual=%d", in.ExpectedRevision, current.Revision)
	}
	if current.Status == model.RuleStatusArchived {
		return OperationResult{}, fmt.Errorf("rule already archived")
	}
	now := s.durableNow()
	current.Status = model.RuleStatusArchived
	current.UpdatedBy = strings.TrimSpace(in.ArchivedBy)
	current.UpdatedAt = now
	current.LastReason = strings.TrimSpace(in.Reason)
	current.ArchivedAt = &now
	current.ArchivedBy = current.UpdatedBy
	current.ArchiveReason = current.LastReason
	if err := model.ValidateRule(current); err != nil {
		return OperationResult{}, err
	}
	payload, err := json.Marshal(current)
	if err != nil {
		return OperationResult{}, err
	}
	opID := durableMutationOperationID(ctx)
	if opID == "" {
		opID, err = s.ruleRevisionOperationID("archive", in)
		if err != nil {
			return OperationResult{}, err
		}
	}
	contract, err := ruleLifecycleContract(previous, current, entity.Payload, payload, in.ExpectedRevision)
	if err != nil {
		return OperationResult{}, err
	}
	if _, err := s.Durability.CommitSharedLifecycleEvent(ctx, sqlitestore.SharedLifecycleEventRequest{
		OperationID:           opID,
		EntityType:            "rule",
		ProjectID:             in.ProjectID,
		EntityID:              id,
		ExpectedRevision:      int64(in.ExpectedRevision),
		ExpectedStoreRevision: entity.Revision,
		ExpectedPayload:       entity.Payload,
		Revision:              int64(current.Revision),
		Kind:                  "rule-archive",
		EventKind:             sqlitestore.SharedLifecycleEventKindArchive,
		HistoryMutationKind:   "archive",
		FromStatus:            previous.Status,
		ToStatus:              current.Status,
		Payload:               payload,
		Actor:                 current.UpdatedBy,
		Reason:                current.LastReason,
		ChangedFields:         []string{"status"},
		Contract:              contract,
		CreatedAt:             current.UpdatedAt,
	}); err != nil {
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

func (s *Service) RuleListPageWithOptions(ctx context.Context, project string, in RuleListInput) (RuleListPageResult, error) {
	if s.Durability == nil {
		return RuleListPageResult{}, fmt.Errorf("rule Shared durability is unavailable")
	}
	page, err := s.querySharedRules(ctx, project, "", "", in.IncludeArchived, sqlitestore.SharedLifecycleQueryMaxRows, in.Cursor)
	if err != nil {
		return RuleListPageResult{}, err
	}
	return RuleListPageResult{
		Rules:      page.Rules,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
	}, nil
}

func (s *Service) RuleQuery(ctx context.Context, project string, in RuleQueryInput) (RuleListPageResult, error) {
	if err := validateEntityProject(project); err != nil {
		return RuleListPageResult{}, err
	}
	if s.Durability == nil {
		return RuleListPageResult{}, fmt.Errorf("rule Shared durability is unavailable")
	}
	text := strings.ToLower(strings.TrimSpace(in.Text))
	status := strings.TrimSpace(in.Status)
	if status != "" && status != model.RuleStatusProposed && status != model.RuleStatusAccepted && status != model.RuleStatusArchived {
		return RuleListPageResult{}, fmt.Errorf("invalid rule status filter")
	}
	page, err := s.querySharedRules(ctx, project, text, status, in.IncludeArchived, sqlitestore.SharedLifecycleQueryMaxRows, in.Cursor)
	if err != nil {
		return RuleListPageResult{}, err
	}
	return RuleListPageResult{
		Rules:      page.Rules,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
	}, nil
}

func (s *Service) RuleHistoryPage(ctx context.Context, project, selector string, in CollectionPageInput, reverse bool) (RuleHistoryResult, error) {
	id, _, err := parseRuleSelector(selector, 0)
	if err != nil {
		return RuleHistoryResult{}, err
	}
	if s.Durability == nil {
		return RuleHistoryResult{}, fmt.Errorf("rule history requires Shared durability")
	}
	if err := s.requireLocalTaskAuthoring(ctx, project); err != nil {
		return RuleHistoryResult{}, err
	}
	kind := "rule-history:" + project + ":" + id
	after := sqlitestore.SharedLifecycleHistoryCursor{}
	if in.Cursor != "" {
		key, decodeErr := pagination.DecodeOpaqueKeyset(in.Cursor, kind)
		if decodeErr != nil {
			return RuleHistoryResult{}, fmt.Errorf("invalid rule history cursor")
		}
		decoded, decodeErr := sqlitestore.DecodeSharedLifecycleHistoryCursor(key)
		if decodeErr != nil {
			return RuleHistoryResult{}, fmt.Errorf("invalid rule history cursor")
		}
		after = decoded
	}
	page, err := s.Durability.ListSharedLifecycleHistoryPage(ctx, "rule", project, id, after, sqlitestore.SharedLifecycleQueryMaxRows)
	if err != nil {
		return RuleHistoryResult{}, err
	}
	result := RuleHistoryResult{
		Key:        id,
		ProjectID:  project,
		Items:      make([]model.RuleHistoryEntry, 0, len(page.Records)),
		CursorKind: kind,
	}
	for _, record := range page.Records {
		if record.EntityID != id || record.ProjectID != project {
			return RuleHistoryResult{}, fmt.Errorf("rule history ownership mismatch")
		}
		result.Items = append(result.Items, model.RuleHistoryEntry{SchemaVersion: model.SchemaVersion, Key: id, ProjectID: project, Revision: int(record.Revision), MutationKind: record.MutationKind, Actor: record.Actor, Reason: record.Reason, ChangedFields: append([]string(nil), record.ChangedFields...), RecordedAt: parseADRTime(record.RecordedAt)})
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

// RuleEffectiveSet projects the accepted named rules of one project into the
// deterministic effective-set membership and returns the internal
// full-strength digest over sorted (name, key, revision) tuples. The aggregate
// workflow-policy document is never consulted; shared_rules is the authority.
func (s *Service) RuleEffectiveSet(ctx context.Context, projectID string) ([]model.Rule, string, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return nil, "", err
	}
	return s.ruleEffectiveSetShared(ctx, projectID)
}

// ruleEffectiveSetShared is the ungated Shared read behind RuleEffectiveSet;
// the workflow-policy derivation uses it directly so the rule authority is
// available for every configured project, not only locally-authored ones.
func (s *Service) ruleEffectiveSetShared(ctx context.Context, projectID string) ([]model.Rule, string, error) {
	if s.Durability == nil {
		return nil, "", fmt.Errorf("rule effective set requires Shared durability")
	}
	entities, err := s.sharedProjectEntities(ctx, "rule", projectID)
	if err != nil {
		return nil, "", err
	}
	effective := make([]model.Rule, 0, len(entities))
	for _, entity := range entities {
		var rule model.Rule
		if err := json.Unmarshal(entity.Payload, &rule); err != nil {
			return nil, "", fmt.Errorf("decode shared rule %s: %w", entity.ID, err)
		}
		if rule.ProjectID != projectID || rule.ID != entity.ID {
			return nil, "", fmt.Errorf("shared rule identity mismatch")
		}
		rule = normalizeRule(rule)
		if err := model.ValidateRule(rule); err != nil {
			return nil, "", err
		}
		if rule.Status == model.RuleStatusAccepted && rule.Name != "" {
			effective = append(effective, rule)
		}
	}
	if len(effective) > ruleEffectiveMaxRules {
		return nil, "", fmt.Errorf("effective rule set exceeds bounded maximum %d", ruleEffectiveMaxRules)
	}
	return effective, ruleEffectiveDigest(effective), nil
}

// ProjectRuleEffectiveDigest is the digest-only freshness authority used by
// the session staleness check and project/status.
func (s *Service) ProjectRuleEffectiveDigest(ctx context.Context, projectID string) (string, error) {
	_, digest, err := s.RuleEffectiveSet(ctx, projectID)
	return digest, err
}

func ruleEffectiveDigest(rules []model.Rule) string {
	members := make([]string, 0, len(rules))
	for _, rule := range rules {
		members = append(members, rule.Name+"\x00"+rule.ID+"\x00"+strconv.Itoa(rule.Revision))
	}
	sort.Strings(members)
	digest := sha256.Sum256([]byte(strings.Join(members, "\x00")))
	return hex.EncodeToString(digest[:])
}
