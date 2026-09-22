package mcp

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func adrActionProperties() map[string]any {
	statuses := outputEnum(sqlitestore.SharedLifecycleStatusValues("adr", false)...)
	return map[string]any{
		"key": str("Stable ADR reference."), "revision": integer("Exact historical ADR revision.", 1, 1000000),
		"title": boundedADRString("ADR title.", 3, 128), "summary": boundedADRString("Bounded ADR summary.", 1, 256),
		"context": boundedADRString("ADR context.", 0, 100000), "decision": boundedADRString("ADR decision.", 0, 100000),
		"consequences": boundedADRString("ADR consequences.", 0, 100000), "status": statuses,
		"reason": boundedADRString("Bounded mutation reason.", 1, 1024), "include_archived": map[string]any{"type": "boolean"},
		"text": str("Case-insensitive text matched across ADR content."), "cursor": str("Opaque server-owned continuation token."),
	}
}

func boundedADRString(description string, min, max int) map[string]any {
	value := str(description)
	value["minLength"], value["maxLength"] = min, max
	return value
}

func adrCreateSchema() map[string]any {
	p := adrActionProperties()
	schema := obj(map[string]any{"title": p["title"], "summary": p["summary"], "context": p["context"], "decision": p["decision"], "consequences": p["consequences"], "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("adr", true)...), "relation_type": outputEnum(model.RelationKindAuthority, model.RelationKindSupersedes), "relation_target": str("Canonical target key for the optional initial relation.")}, "title", "summary", "context", "decision", "consequences")
	return schema
}
func adrReadSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"key": p["key"], "revision": p["revision"]}, "key")
}
func adrListSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"cursor": p["cursor"], "include_archived": p["include_archived"]})
}
func adrQuerySchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"cursor": p["cursor"], "text": p["text"], "status": p["status"]})
}
func adrUpdateSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"key": p["key"], "title": p["title"], "summary": p["summary"], "context": p["context"], "decision": p["decision"], "consequences": p["consequences"], "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("adr", false)...), "reason": p["reason"]}, "key", "reason")
}
func adrArchiveSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"key": p["key"], "reason": p["reason"]}, "key", "reason")
}
func adrHistorySchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"key": p["key"], "cursor": p["cursor"]}, "key")
}
func adrLegacyRelationsSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"key": p["key"], "cursor": p["cursor"]})
}
func adrLegacyRelationsOutputSchema() map[string]any {
	relation := closedOutput(map[string]any{"key": outputString(), "revision": outputInteger(), "supersedes": outputString()}, "key", "revision", "supersedes")
	return closedOutput(map[string]any{"items": outputArray(relation)}, "items")
}
func adrExecutionSchema(public map[string]any) map[string]any {
	props := map[string]any{"project_id": str("Session-derived project identity.")}
	if p, ok := public["properties"].(map[string]any); ok {
		for k, v := range p {
			props[k] = v
		}
	}
	req := []string{"project_id"}
	if r, ok := public["required"].([]string); ok {
		req = append(req, r...)
	}
	return closedOutput(props, req...)
}
func adrMutationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "revision": outputInteger()}, "key", "revision")
}
func adrHistoryOutputSchema() map[string]any {
	r := closedOutput(map[string]any{"revision": outputInteger(), "mutation_kind": outputString(), "actor": outputString(), "reason": outputString(), "changed_fields": outputArray(outputString()), "recorded_at": outputDateTime()}, "revision", "mutation_kind", "actor", "reason", "recorded_at")
	return closedOutput(map[string]any{"key": outputString(), "items": outputArray(r)}, "key", "items")
}
func adrListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"items": outputArray(adrSummaryOutputSchema())}, "items")
}
func adrSummaryOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "title": outputString(), "summary": outputString(), "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("adr", false)...), "revision": outputInteger(), "updated_at": outputDateTime()}, "key", "title", "summary", "status", "revision")
}
func adrPublicUpdateVisible(v model.ADR) bool {
	return !v.UpdatedAt.IsZero() && !v.UpdatedAt.Equal(v.CreatedAt)
}

func adrPublicProjection(v model.ADR, relations map[string]map[string]string) map[string]any {
	result := map[string]any{"key": v.ID, "revision": v.Revision, "title": v.Title, "status": v.Status, "context": v.Context, "decision": v.Decision, "consequences": v.Consequences, "created_at": v.CreatedAt, "relations": relationGroupedValue(relations)}
	if v.Summary != "" {
		result["summary"] = v.Summary
	}
	if adrPublicUpdateVisible(v) {
		result["updated_at"], result["revision_reason"] = v.UpdatedAt, v.LastReason
	}
	return result
}
func adrMutationPublicValue(v service.OperationResult) map[string]any {
	return map[string]any{"key": v.EntityKey, "revision": v.Revision}
}

func adrPublicPageCandidate(p service.ADRListPageResult, count int) (map[string]any, string, error) {
	items := make([]any, 0, count)
	for _, v := range p.ADRs[:count] {
		item := map[string]any{"key": v.ID, "title": v.Title, "summary": v.Summary, "status": v.Status, "revision": v.Revision}
		if adrPublicUpdateVisible(v) {
			item["updated_at"] = v.UpdatedAt
		}
		items = append(items, item)
	}
	result := map[string]any{"items": items}
	if p.HasMore || count < len(p.ADRs) {
		next := p.NextCursor
		if count < len(p.ADRs) {
			next = pagination.EncodeServerCursor(p.CursorKind, p.ADRs[count-1].ID)
		}
		if next == "" {
			return nil, "", fmt.Errorf("ADR pagination invariant: continuation is empty")
		}
		return result, next, nil
	}
	return result, "", nil
}

func adrHistoryPageCandidate(p service.ADRHistoryResult, count int) (map[string]any, string, error) {
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
			return nil, "", fmt.Errorf("ADR history pagination invariant: continuation is not server-owned")
		}
		if next == "" {
			return nil, "", fmt.Errorf("ADR history pagination invariant: continuation is empty")
		}
		return result, pagination.EncodeOpaqueKeyset(p.CursorKind, next), nil
	}
	return result, "", nil
}

func adrPublicPageValue(p service.ADRListPageResult) (genericActionContinuation, error) {
	if len(p.ADRs) == 0 {
		if p.HasMore {
			return genericActionContinuation{}, fmt.Errorf("ADR pagination invariant: empty page has continuation")
		}
		return genericActionPageResult(map[string]any{"items": []any{}}, false, "")
	}
	count, err := service.LargestPublicPageSize(len(p.ADRs), func(count int) (bool, error) {
		candidate, _, err := adrPublicPageCandidate(p, count)
		if err != nil {
			return false, err
		}
		return service.PublicPageFitsTokenBudget(candidate)
	})
	if err != nil {
		return genericActionContinuation{}, err
	}
	if count == 0 {
		return genericActionContinuation{}, fmt.Errorf("ADR semantic unit exceeds the token budget")
	}
	candidate, cursor, err := adrPublicPageCandidate(p, count)
	if err != nil {
		return genericActionContinuation{}, err
	}
	return genericActionPageResult(candidate, p.HasMore || count < len(p.ADRs), cursor)
}

func adrHistoryPageValue(p service.ADRHistoryResult) (genericActionContinuation, error) {
	if len(p.Items) == 0 {
		if p.HasMore {
			return genericActionContinuation{}, fmt.Errorf("ADR history pagination invariant: empty page has continuation")
		}
		return genericActionPageResult(map[string]any{"key": p.Key, "items": []any{}}, false, "")
	}
	result, cursor, err := adrHistoryPageCandidate(p, len(p.Items))
	if err != nil {
		return genericActionContinuation{}, err
	}
	return genericActionPageResult(result, p.HasMore, cursor)
}
