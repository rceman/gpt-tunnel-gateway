package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func rev7ADRCaller(t *testing.T) func(int, string, map[string]any) map[string]any {
	t.Helper()
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	return func(id int, action string, input map[string]any) map[string]any {
		t.Helper()
		return genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": action, "input": input,
			}},
		})))
	}
}

func rev7ADRInput(title, summary string) map[string]any {
	return map[string]any{"title": title, "summary": summary, "context": "context", "decision": "decision", "consequences": "consequences"}
}

func TestTSK409Rev7ADRReadOmitsAbsentLegacySummary(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	call := func(id int, action string, input map[string]any) map[string]any {
		t.Helper()
		return genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": action, "input": input,
			}},
		})))
	}
	created := call(1, "adr/create", rev7ADRInput("Legacy summary read decision", "Bounded current summary"))
	key := created["result"].(map[string]any)["key"].(string)
	current := call(2, "adr/read", map[string]any{"key": key})
	if current["result"].(map[string]any)["summary"] != "Bounded current summary" {
		t.Fatalf("current adr/read summary=%#v", current["result"])
	}
	ctx := context.Background()
	payloads, err := server.Service.Durability.ListSharedLifecycleHistoryPage(ctx, "adr", "example", key, sqlitestore.SharedLifecycleHistoryCursor{}, sqlitestore.SharedLifecycleQueryMaxRows)
	if err != nil || len(payloads.Records) == 0 {
		t.Fatalf("legacy history read=%#v err=%v", payloads.Records, err)
	}
	var legacy model.ADR
	if err := json.Unmarshal(payloads.Records[0].Payload, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Summary = ""
	legacyPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Service.Durability.Shared.Exec(ctx, `UPDATE shared_entity_revisions SET payload=? WHERE entity_type='adr' AND entity_id=? AND revision=?`, legacyPayload, key, legacy.Revision); err != nil {
		t.Fatal(err)
	}
	historical := call(3, "adr/read", map[string]any{"key": key, "revision": legacy.Revision})
	historicalResult := historical["result"].(map[string]any)
	if _, exists := historicalResult["summary"]; exists {
		t.Fatalf("legacy adr/read revision exposed an empty summary: %#v", historicalResult)
	}
	if historicalResult["title"] != legacy.Title || historicalResult["revision"] != float64(legacy.Revision) {
		t.Fatalf("legacy adr/read revision=%#v", historicalResult)
	}
	server.ensureADRActions()
	entries := server.genericActionRegistry(nil)
	listItems := tsk409SchemaProperties(entries["adr/list"].OutputSchema)["items"].(map[string]any)["items"].(map[string]any)
	if !tsk409SchemaRequires(listItems, "summary") {
		t.Fatalf("adr/list item schema does not require summary: %#v", listItems)
	}
	if tsk409SchemaRequires(entries["adr/read"].OutputSchema, "summary") {
		t.Fatal("adr/read output schema requires summary for legacy history")
	}
	if _, declared := tsk409SchemaProperties(entries["adr/read"].OutputSchema)["summary"]; !declared {
		t.Fatal("adr/read output schema does not declare summary")
	}
}

func tsk409SchemaRequires(schema map[string]any, field string) bool {
	required, _ := schema["required"].([]string)
	for _, name := range required {
		if name == field {
			return true
		}
	}
	return false
}

func TestTSK409Rev7ADRListAndQueryExposeUniversalItems(t *testing.T) {
	call := rev7ADRCaller(t)
	first := call(1, "adr/create", rev7ADRInput("Universal items decision", "Bounded universal items summary"))
	if first["is_error"] == true {
		t.Fatalf("adr/create failed: %#v", first)
	}
	firstKey := first["result"].(map[string]any)["key"].(string)
	second := call(2, "adr/create", rev7ADRInput("Second universal decision", "Bounded second summary"))
	secondKey := second["result"].(map[string]any)["key"].(string)

	listed := call(3, "adr/list", map[string]any{})
	if listed["is_error"] == true {
		t.Fatalf("adr/list failed: %#v", listed)
	}
	listResult := listed["result"].(map[string]any)
	for _, forbidden := range []string{"adrs", "adr", "adr_id", "adr_key", "id", "items_"} {
		if _, exists := listResult[forbidden]; exists {
			t.Fatalf("adr/list exposed %q: %#v", forbidden, listResult)
		}
	}
	items, ok := listResult["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("adr/list items=%#v", listResult)
	}
	seen := map[string]bool{}
	for _, raw := range items {
		item := raw.(map[string]any)
		if _, exists := item["adr"]; exists {
			t.Fatalf("adr/list item exposed adr: %#v", item)
		}
		for _, field := range []string{"key", "title", "summary", "status", "revision"} {
			if _, exists := item[field]; !exists {
				t.Fatalf("adr/list item omitted %q: %#v", field, item)
			}
		}
		seen[item["key"].(string)] = true
	}
	if !seen[firstKey] || !seen[secondKey] {
		t.Fatalf("adr/list keys=%#v want %s and %s", seen, firstKey, secondKey)
	}

	queried := call(4, "adr/query", map[string]any{"text": "universal items"})
	if queried["is_error"] == true {
		t.Fatalf("adr/query failed: %#v", queried)
	}
	queryResult := queried["result"].(map[string]any)
	if _, exists := queryResult["adrs"]; exists {
		t.Fatalf("adr/query exposed adrs: %#v", queryResult)
	}
	queryItems := queryResult["items"].([]any)
	if len(queryItems) != 1 || queryItems[0].(map[string]any)["key"] != firstKey {
		t.Fatalf("adr/query items=%#v", queryItems)
	}
	if queryItems[0].(map[string]any)["summary"] != "Bounded universal items summary" {
		t.Fatalf("adr/query summary=%#v", queryItems[0])
	}
}

func TestTSK409Rev7ADRArchiveKeepsRevisionAndScopesListing(t *testing.T) {
	call := rev7ADRCaller(t)
	created := call(1, "adr/create", rev7ADRInput("Archived universal decision", "Bounded archived summary"))
	key := created["result"].(map[string]any)["key"].(string)
	archived := call(2, "adr/archive", map[string]any{"key": key, "reason": "archive the decision"})
	if archived["is_error"] == true {
		t.Fatalf("adr/archive failed: %#v", archived)
	}
	archiveResult := archived["result"].(map[string]any)
	if archiveResult["key"] != key || archiveResult["revision"] != float64(1) {
		t.Fatalf("adr/archive receipt=%#v", archiveResult)
	}
	current := call(3, "adr/read", map[string]any{"key": key})
	if result := current["result"].(map[string]any); result["status"] != model.ADRStatusArchived || result["revision"] != float64(1) {
		t.Fatalf("archived adr/read=%#v", result)
	}
	historical := call(4, "adr/read", map[string]any{"key": key, "revision": 1})
	if result := historical["result"].(map[string]any); result["status"] != model.ADRStatusProposed {
		t.Fatalf("historical adr/read=%#v", result)
	}
	listed := call(5, "adr/list", map[string]any{})
	if items := listed["result"].(map[string]any)["items"].([]any); len(items) != 0 {
		t.Fatalf("adr/list included archived ADRs=%#v", items)
	}
	included := call(6, "adr/list", map[string]any{"include_archived": true})
	if items := included["result"].(map[string]any)["items"].([]any); len(items) != 1 || items[0].(map[string]any)["status"] != model.ADRStatusArchived {
		t.Fatalf("adr/list include_archived=%#v", items)
	}
	history := call(7, "adr/history", map[string]any{"key": key})
	historyResult := history["result"].(map[string]any)
	if historyResult["key"] != key {
		t.Fatalf("adr/history key=%#v", historyResult)
	}
	historyItems := historyResult["items"].([]any)
	if len(historyItems) != 2 || historyItems[1].(map[string]any)["mutation_kind"] != "archive" || historyItems[1].(map[string]any)["revision"] != float64(1) {
		t.Fatalf("adr/history items=%#v", historyItems)
	}
}

func TestTSK409Rev7ADRRevisionOneStatusProjectionExposesUpdate(t *testing.T) {
	call := rev7ADRCaller(t)
	created := call(1, "adr/create", rev7ADRInput("Timestamp projection decision", "Bounded timestamp summary"))
	key := created["result"].(map[string]any)["key"].(string)
	fresh := call(2, "adr/read", map[string]any{"key": key})
	freshResult := fresh["result"].(map[string]any)
	if _, exists := freshResult["updated_at"]; exists {
		t.Fatalf("fresh ADR exposed updated_at: %#v", freshResult)
	}
	if _, exists := freshResult["revision_reason"]; exists {
		t.Fatalf("fresh ADR exposed revision_reason: %#v", freshResult)
	}
	updated := call(3, "adr/update", map[string]any{"key": key, "status": model.ADRStatusAccepted, "reason": "accept projection decision"})
	if updated["is_error"] == true {
		t.Fatalf("adr/update failed: %#v", updated)
	}
	if result := updated["result"].(map[string]any); result["revision"] != float64(1) {
		t.Fatalf("revision-1 status transition receipt=%#v", result)
	}
	current := call(4, "adr/read", map[string]any{"key": key})
	currentResult := current["result"].(map[string]any)
	if currentResult["revision"] != float64(1) || currentResult["status"] != model.ADRStatusAccepted {
		t.Fatalf("revision-1 status read=%#v", currentResult)
	}
	if currentResult["updated_at"] == nil || currentResult["revision_reason"] != "accept projection decision" {
		t.Fatalf("revision-1 status read omitted the transition timestamp: %#v", currentResult)
	}
	if currentResult["updated_at"] == currentResult["created_at"] {
		t.Fatalf("revision-1 status read reused created_at: %#v", currentResult)
	}
	listed := call(5, "adr/list", map[string]any{})
	items := listed["result"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["updated_at"] == nil {
		t.Fatalf("revision-1 status list item omitted updated_at: %#v", items)
	}
}

func rev7ADRWalkCaller(t *testing.T) func(int, string, map[string]any) map[string]any {
	t.Helper()
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	return func(id int, action string, input map[string]any) map[string]any {
		t.Helper()
		response := callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": action, "input": input,
			}},
		}))
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("missing MCP result: %#v", response)
		}
		structured, ok := result["structuredContent"].(map[string]any)
		if !ok {
			t.Fatalf("missing structured content: %#v", response)
		}
		return structured
	}
}

func TestTSK409Rev7ADRCompleteListWalkVisitsEveryADROnce(t *testing.T) {
	call := rev7ADRWalkCaller(t)
	const total = 40
	expected := map[string]bool{}
	for index := 0; index < total; index++ {
		title := fmt.Sprintf("%04d", index) + strings.Repeat("w", 124)
		envelope := call(index+1, "adr/create", rev7ADRInput(title, strings.Repeat("s", 256)))
		if envelope["ok"] != true {
			t.Fatalf("adr/create %d failed: %#v", index, envelope)
		}
		expected[envelope["result"].(map[string]any)["key"].(string)] = true
	}
	seen := map[string]int{}
	cursor := ""
	pages := 0
	for {
		input := map[string]any{}
		if cursor != "" {
			input["cursor"] = cursor
		}
		envelope := call(total+1+pages, "adr/list", input)
		if envelope["ok"] != true {
			t.Fatalf("adr/list page %d failed: %#v", pages, envelope)
		}
		result, ok := envelope["result"].(map[string]any)
		if !ok {
			t.Fatalf("adr/list page %d has no result: %#v", pages, envelope)
		}
		if _, exists := result["adrs"]; exists {
			t.Fatalf("adr/list page %d exposed adrs: %#v", pages, result)
		}
		if _, exists := result["_pagination"]; exists {
			t.Fatalf("adr/list page %d leaked private pagination: %#v", pages, result)
		}
		for _, raw := range result["items"].([]any) {
			seen[raw.(map[string]any)["key"].(string)]++
		}
		pages++
		paginationValue, hasMore := envelope["pagination"].(map[string]any)
		if !hasMore {
			break
		}
		cursor = paginationValue["next_cursor"].(string)
		if cursor == "" {
			t.Fatalf("adr/list page %d returned an empty continuation", pages)
		}
		if pages > total {
			t.Fatalf("adr/list walk did not terminate after %d pages", pages)
		}
	}
	if pages < 2 {
		t.Fatalf("adr/list walk used %d page(s); the budget did not force pagination", pages)
	}
	if len(seen) != total {
		t.Fatalf("adr/list walk saw %d distinct ADRs, want %d", len(seen), total)
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("adr/list walk duplicated %s: %#v", key, seen)
		}
		if !expected[key] {
			t.Fatalf("adr/list walk saw an unexpected ADR %s", key)
		}
	}
}

func TestTSK409Rev7ADRListProjectionPaginatesWithServerOwnedCursor(t *testing.T) {
	const kind = "adr-list:example"
	adrs := make([]model.ADR, 0, 24)
	now := time.Date(2026, 9, 11, 17, 0, 0, 0, time.UTC)
	for index := 0; index < 24; index++ {
		adrs = append(adrs, model.ADR{SchemaVersion: model.SchemaVersion, ID: fmt.Sprintf("EXM-ADR%02d", index), ProjectID: "example",
			Revision: 1, RevisionCount: 1, Title: strings.Repeat("t", 120) + fmt.Sprintf("%04d", index), Summary: strings.Repeat("s", 256),
			Status: model.ADRStatusProposed, Context: "context", Decision: "decision", Consequences: "consequences", CreatedAt: now, UpdatedAt: now})
	}
	page, err := adrPublicPageValue(service.ADRListPageResult{ADRs: adrs, NextCursor: "unused", HasMore: true, CursorKind: kind})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := page["items"].([]any)
	if !ok || len(items) == 0 || len(items) >= len(adrs) {
		t.Fatalf("truncated page items=%d err=%v", len(items), err)
	}
	paginationValue, ok := page["_pagination"].(map[string]any)
	if !ok {
		t.Fatalf("truncated page omitted pagination: %#v", page)
	}
	cursor := paginationValue["next_cursor"].(string)
	last := items[len(items)-1].(map[string]any)["key"].(string)
	decoded, ok := pagination.ResolveServerCursor(cursor, kind)
	if !ok || decoded != last {
		t.Fatalf("cursor resolved=%q ok=%v want %q", decoded, ok, last)
	}
	if _, exists := page["adrs"]; exists {
		t.Fatalf("truncated page exposed adrs: %#v", page)
	}

	final, err := adrPublicPageValue(service.ADRListPageResult{ADRs: adrs[:3], CursorKind: kind})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := final["_pagination"]; exists {
		t.Fatalf("terminal page exposed pagination: %#v", final)
	}
	if terminalItems := final["items"].([]any); len(terminalItems) != 3 {
		t.Fatalf("terminal page items=%d want 3", len(terminalItems))
	}
	remaining := adrs[len(items):]
	walked := len(items)
	for len(remaining) > 0 {
		next, err := adrPublicPageValue(service.ADRListPageResult{ADRs: remaining, NextCursor: "unused", HasMore: true, CursorKind: kind})
		if err != nil {
			t.Fatal(err)
		}
		count := len(next["items"].([]any))
		if count == 0 || count > len(remaining) {
			t.Fatalf("page walk stalled: count=%d remaining=%d", count, len(remaining))
		}
		if count == len(remaining) {
			break
		}
		walked += count
		remaining = remaining[count:]
	}
	if walked >= len(adrs) {
		t.Fatalf("page walk consumed %d of %d ADRs without terminating", walked, len(adrs))
	}
}
