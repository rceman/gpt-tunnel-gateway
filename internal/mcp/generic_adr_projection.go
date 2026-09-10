package mcp

import (
	"fmt"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func adrActionProperties() map[string]any {
	return map[string]any{
		"adr": str("Stable ADR reference."), "revision": integer("Exact historical ADR revision.", 1, 1000000),
		"title": boundedADRString("ADR title.", 3, 300), "context": boundedADRString("ADR context.", 0, 100000), "decision": boundedADRString("ADR decision.", 0, 100000),
		"consequences": boundedADRString("ADR consequences.", 0, 100000), "status": outputEnum("proposed", "accepted", "superseded", "archived"),
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
	return obj(map[string]any{"title": p["title"], "context": p["context"], "decision": p["decision"], "consequences": p["consequences"], "status": p["status"]}, "title", "context", "decision", "consequences")
}
func adrReadSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"adr": p["adr"], "revision": p["revision"]}, "adr")
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
	return obj(map[string]any{"adr": p["adr"], "title": p["title"], "context": p["context"], "decision": p["decision"], "consequences": p["consequences"], "status": p["status"], "reason": p["reason"]}, "adr", "reason")
}
func adrArchiveSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"adr": p["adr"], "reason": p["reason"]}, "adr", "reason")
}
func adrHistorySchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"adr": p["adr"], "cursor": p["cursor"]}, "adr")
}
func adrLegacyRelationsSchema() map[string]any {
	p := adrActionProperties()
	return obj(map[string]any{"adr": p["adr"], "cursor": p["cursor"]})
}
func adrLegacyRelationsOutputSchema() map[string]any {
	relation := closedOutput(map[string]any{"adr": outputString(), "revision": outputInteger(), "supersedes": outputString()}, "adr", "revision", "supersedes")
	return closedOutput(map[string]any{"relations": outputArray(relation)}, "relations")
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
	return closedOutput(map[string]any{"adr": outputString(), "revision": outputInteger()}, "adr", "revision")
}
func adrHistoryOutputSchema() map[string]any {
	r := closedOutput(map[string]any{"revision": outputInteger(), "mutation_kind": outputString(), "actor": outputString(), "reason": outputString(), "changed_fields": outputArray(outputString()), "recorded_at": outputDateTime()}, "revision", "mutation_kind", "actor", "reason", "recorded_at")
	return closedOutput(map[string]any{"adr": outputString(), "revisions": outputArray(r)}, "adr", "revisions")
}
func adrListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"adrs": outputArray(adrSummaryOutputSchema())}, "adrs")
}
func adrSummaryOutputSchema() map[string]any {
	return closedOutput(map[string]any{"adr": outputString(), "title": outputString(), "status": outputEnum("proposed", "accepted", "superseded", "archived"), "revision": outputInteger(), "updated_at": outputDateTime()}, "adr", "title", "status", "revision")
}
func adrPublicProjection(v model.ADR) map[string]any {
	result := map[string]any{"adr": v.ID, "revision": v.Revision, "title": v.Title, "status": v.Status, "context": v.Context, "decision": v.Decision, "consequences": v.Consequences, "created_at": v.CreatedAt}
	if v.Revision >= 2 {
		result["updated_at"], result["revision_reason"] = v.UpdatedAt, v.LastReason
	}
	return result
}
func adrMutationPublicValue(v service.OperationResult) map[string]any {
	return map[string]any{"adr": v.EntityKey, "revision": v.Revision}
}

func adrPublicPageCandidate(p service.ADRListPageResult, count int) (map[string]any, error) {
	items := make([]any, 0, count)
	for _, v := range p.ADRs[:count] {
		item := map[string]any{"adr": v.ID, "title": v.Title, "status": v.Status, "revision": v.Revision}
		if v.Revision >= 2 {
			item["updated_at"] = v.UpdatedAt
		}
		items = append(items, item)
	}
	result := map[string]any{"adrs": items}
	if p.HasMore || count < len(p.ADRs) {
		next := p.NextCursor
		if count < len(p.ADRs) {
			next = pagination.EncodeOpaqueKeyset(p.CursorKind, p.ADRs[count-1].ID)
		}
		if next == "" {
			return nil, fmt.Errorf("ADR pagination invariant: continuation is empty")
		}
		result["_pagination"] = map[string]any{"next_cursor": next}
	}
	return result, nil
}

func adrHistoryPageCandidate(p service.ADRHistoryResult, count int) (map[string]any, error) {
	items := make([]any, 0, count)
	for _, v := range p.Revisions[:count] {
		item := map[string]any{"revision": v.Revision, "mutation_kind": v.MutationKind, "actor": v.Actor, "reason": v.Reason, "recorded_at": v.RecordedAt}
		if len(v.ChangedFields) > 0 {
			item["changed_fields"] = v.ChangedFields
		}
		items = append(items, item)
	}
	result := map[string]any{"adr": p.ADRID, "revisions": items}
	if p.HasMore || count < len(p.Revisions) {
		next := p.NextCursor
		if count < len(p.Revisions) {
			next = pagination.EncodeOpaqueKeyset(p.CursorKind, strconv.Itoa(p.Revisions[count-1].Revision))
		}
		if next == "" {
			return nil, fmt.Errorf("ADR history pagination invariant: continuation is empty")
		}
		result["_pagination"] = map[string]any{"next_cursor": next}
	}
	return result, nil
}

func adrPublicPageValue(p service.ADRListPageResult) (map[string]any, error) {
	if len(p.ADRs) == 0 {
		if p.HasMore {
			return nil, fmt.Errorf("ADR pagination invariant: empty page has continuation")
		}
		return map[string]any{"adrs": []any{}}, nil
	}
	count, err := service.LargestPublicPageSize(len(p.ADRs), func(count int) (bool, error) {
		candidate, err := adrPublicPageCandidate(p, count)
		if err != nil {
			return false, err
		}
		return service.PublicPageFitsTokenBudget(candidate)
	})
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("ADR semantic unit exceeds the token budget")
	}
	return adrPublicPageCandidate(p, count)
}

func adrHistoryPageValue(p service.ADRHistoryResult) (map[string]any, error) {
	if len(p.Revisions) == 0 {
		if p.HasMore {
			return nil, fmt.Errorf("ADR history pagination invariant: empty page has continuation")
		}
		return map[string]any{"adr": p.ADRID, "revisions": []any{}}, nil
	}
	count, err := service.LargestPublicPageSize(len(p.Revisions), func(count int) (bool, error) {
		candidate, err := adrHistoryPageCandidate(p, count)
		if err != nil {
			return false, err
		}
		return service.PublicPageFitsTokenBudget(candidate)
	})
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("ADR history semantic unit exceeds the token budget")
	}
	return adrHistoryPageCandidate(p, count)
}
