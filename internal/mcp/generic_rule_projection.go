package mcp

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func ruleActionProperties() map[string]any {
	statuses := outputEnum(sqlitestore.SharedLifecycleStatusValues("rule", false)...)
	return map[string]any{
		"key": str("Stable rule reference."), "revision": integer("Exact historical rule revision.", 1, 1000000),
		"title": boundedADRString("Rule title.", 1, model.RuleTitleMaxRunes), "summary": boundedADRString("Bounded rule summary.", 1, model.RuleSummaryMaxRunes),
		"name":        map[string]any{"type": "string", "description": "Immutable lowercase machine identity; segments [a-z0-9_] optionally separated by single dots.", "pattern": `^[a-z0-9_]+(\.[a-z0-9_]+)*$`, "maxLength": model.RuleNameMaxRunes},
		"value":       map[string]any{"description": "Typed JSON rule value (string, number, boolean, object, or array); required iff name is present."},
		"description": boundedADRString("Rule narrative body.", 1, model.RuleDescriptionMaxRunes),
		"status":      statuses, "reason": boundedADRString("Bounded mutation reason.", 1, 1024),
		"include_archived": map[string]any{"type": "boolean"},
		"text":             str("Case-insensitive text matched across rule content."), "cursor": publicServerCursorSchema(),
	}
}

func ruleCreateSchema() map[string]any {
	p := ruleActionProperties()
	return obj(map[string]any{"title": p["title"], "summary": p["summary"], "name": p["name"], "value": p["value"], "description": p["description"], "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("rule", true)...), "relation_type": outputEnum(model.RelationKindAuthority), "relation_target": str("Canonical target key for the optional initial relation.")}, "title", "summary")
}
func ruleReadSchema() map[string]any {
	p := ruleActionProperties()
	return obj(map[string]any{"key": p["key"], "revision": p["revision"]}, "key")
}
func ruleListSchema() map[string]any {
	p := ruleActionProperties()
	return obj(map[string]any{"cursor": p["cursor"], "include_archived": p["include_archived"]})
}
func ruleQuerySchema() map[string]any {
	p := ruleActionProperties()
	return obj(map[string]any{"cursor": p["cursor"], "text": p["text"], "status": p["status"]})
}
func ruleUpdateSchema() map[string]any {
	p := ruleActionProperties()
	return obj(map[string]any{"key": p["key"], "title": p["title"], "summary": p["summary"], "value": p["value"], "description": p["description"], "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("rule", false)...), "reason": p["reason"]}, "key", "reason")
}
func ruleArchiveSchema() map[string]any {
	p := ruleActionProperties()
	return obj(map[string]any{"key": p["key"], "reason": p["reason"]}, "key", "reason")
}
func ruleHistorySchema() map[string]any {
	p := ruleActionProperties()
	return obj(map[string]any{"key": p["key"], "cursor": p["cursor"]}, "key")
}
func ruleEffectiveSchema() map[string]any {
	return obj(map[string]any{})
}
func ruleEffectiveExecutionSchema() map[string]any {
	return closedOutput(map[string]any{"project_id": str("Session-derived project identity.")}, "project_id")
}
func ruleOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"key": outputString(), "revision": outputInteger(), "title": outputString(), "summary": outputString(), "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("rule", false)...),
		"name": outputString(), "value": map[string]any{"description": "Typed JSON rule value."}, "description": outputString(), "created_at": outputDateTime(),
		"updated_at": outputDateTime(), "revision_reason": outputString(), "relations": relationGroupedOutputSchema(),
	}, "key", "revision", "title", "status", "created_at", "relations")
}
func ruleEffectiveOutputSchema() map[string]any {
	return closedOutput(map[string]any{"digest": outputString(), "rules": map[string]any{"type": "object", "additionalProperties": true}, "acknowledged": outputBoolean()}, "digest", "rules", "acknowledged")
}
func ruleMutationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "revision": outputInteger()}, "key", "revision")
}
func ruleHistoryOutputSchema() map[string]any {
	r := closedOutput(map[string]any{"revision": outputInteger(), "mutation_kind": outputString(), "actor": outputString(), "reason": outputString(), "changed_fields": outputArray(outputString()), "recorded_at": outputDateTime()}, "revision", "mutation_kind", "actor", "reason", "recorded_at")
	return closedOutput(map[string]any{"key": outputString(), "items": outputArray(r)}, "key", "items")
}
func ruleListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"items": outputArray(ruleSummaryOutputSchema())}, "items")
}
func ruleSummaryOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "title": outputString(), "summary": outputString(), "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("rule", false)...), "name": outputString(), "revision": outputInteger(), "updated_at": outputDateTime()}, "key", "title", "summary", "status", "revision")
}

func rulePublicUpdateVisible(v model.Rule) bool {
	return !v.UpdatedAt.IsZero() && !v.UpdatedAt.Equal(v.CreatedAt)
}

func rulePublicProjection(v model.Rule, relations map[string]map[string]string) map[string]any {
	result := map[string]any{"key": v.ID, "revision": v.Revision, "title": v.Title, "status": v.Status, "created_at": v.CreatedAt, "relations": relationGroupedValue(relations)}
	if v.Summary != "" {
		result["summary"] = v.Summary
	}
	if v.Name != "" {
		result["name"] = v.Name
		result["value"] = v.Value
	}
	if v.Description != "" {
		result["description"] = v.Description
	}
	if rulePublicUpdateVisible(v) {
		result["updated_at"], result["revision_reason"] = v.UpdatedAt, v.LastReason
	}
	return result
}

func ruleMutationPublicValue(v service.OperationResult) map[string]any {
	return map[string]any{"key": v.EntityKey, "revision": v.Revision}
}

func rulePublicPageCandidate(p service.RuleListPageResult, count int) (map[string]any, string, error) {
	items := make([]any, 0, count)
	for _, v := range p.Rules[:count] {
		item := map[string]any{"key": v.ID, "title": v.Title, "summary": v.Summary, "status": v.Status, "revision": v.Revision}
		if v.Name != "" {
			item["name"] = v.Name
		}
		if rulePublicUpdateVisible(v) {
			item["updated_at"] = v.UpdatedAt
		}
		items = append(items, item)
	}
	result := map[string]any{"items": items}
	if p.HasMore || count < len(p.Rules) {
		next := p.NextCursor
		if count < len(p.Rules) {
			next = pagination.EncodeServerCursor(p.CursorKind, p.Rules[count-1].ID)
		}
		if next == "" {
			return nil, "", fmt.Errorf("rule pagination invariant: continuation is empty")
		}
		return result, next, nil
	}
	return result, "", nil
}

func ruleHistoryPageCandidate(p service.RuleHistoryResult, count int) (map[string]any, string, error) {
	items := make([]any, 0, count)
	for _, v := range p.Items[:count] {
		item := map[string]any{"revision": v.Revision, "mutation_kind": v.MutationKind, "actor": v.Actor, "reason": v.Reason, "recorded_at": v.RecordedAt}
		if len(v.ChangedFields) > 0 {
			item["changed_fields"] = v.ChangedFields
		}
		items = append(items, item)
	}
	result := map[string]any{"key": p.Key, "items": items}
	if p.HasMore || count < len(p.Items) {
		next := p.NextCursor
		if count < len(p.Items) {
			return nil, "", fmt.Errorf("rule history pagination invariant: continuation is not server-owned")
		}
		if next == "" {
			return nil, "", fmt.Errorf("rule history pagination invariant: continuation is empty")
		}
		return result, pagination.EncodeOpaqueKeyset(p.CursorKind, next), nil
	}
	return result, "", nil
}

func rulePublicPageValue(p service.RuleListPageResult) (genericActionContinuation, error) {
	if len(p.Rules) == 0 {
		if p.HasMore {
			return genericActionContinuation{}, fmt.Errorf("rule pagination invariant: empty page has continuation")
		}
		return genericActionPageResult(map[string]any{"items": []any{}}, false, "")
	}
	count, err := service.LargestPublicPageSize(len(p.Rules), func(count int) (bool, error) {
		candidate, _, err := rulePublicPageCandidate(p, count)
		if err != nil {
			return false, err
		}
		return service.PublicPageFitsTokenBudget(candidate)
	})
	if err != nil {
		return genericActionContinuation{}, err
	}
	if count == 0 {
		return genericActionContinuation{}, fmt.Errorf("rule semantic unit exceeds the token budget")
	}
	candidate, cursor, err := rulePublicPageCandidate(p, count)
	if err != nil {
		return genericActionContinuation{}, err
	}
	return genericActionPageResult(candidate, p.HasMore || count < len(p.Rules), cursor)
}

func ruleHistoryPageValue(p service.RuleHistoryResult) (genericActionContinuation, error) {
	if len(p.Items) == 0 {
		if p.HasMore {
			return genericActionContinuation{}, fmt.Errorf("rule history pagination invariant: empty page has continuation")
		}
		return genericActionPageResult(map[string]any{"key": p.Key, "items": []any{}}, false, "")
	}
	result, cursor, err := ruleHistoryPageCandidate(p, len(p.Items))
	if err != nil {
		return genericActionContinuation{}, err
	}
	return genericActionPageResult(result, p.HasMore, cursor)
}
