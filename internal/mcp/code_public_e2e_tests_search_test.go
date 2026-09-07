package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

func TestPublicCodeActionsE2EPerformanceAndPagination(t *testing.T) {
	if codeOutputTokenCeiling != 3000 || tokenizer.MaxTokens != 3000 {
		t.Fatalf("code output token ceiling drifted: runtime=%d tokenizer=%d", codeOutputTokenCeiling, tokenizer.MaxTokens)
	}
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)

	worktreePage := harness.callPage(t, "code/worktree", map[string]any{})
	worktree := worktreePage.result
	items, ok := worktree["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("code/worktree first page=%#v", worktree)
	}
	firstItem := items[0].(map[string]any)
	worktreePagination := worktreePage.pagination
	if firstItem["head"] != fixture.currentHead {
		t.Fatalf("code/worktree first page lacks exact head: %#v", worktree)
	}
	if worktreePagination != nil {
		t.Fatalf("code/worktree was item-capped instead of token-packed: %#v", worktree)
	}

	treePage := harness.callPage(t, "code/tree", map[string]any{"worktree": fixture.mainSelector, "live": true})
	tree := treePage.result
	assertPublicCodeHead(t, tree, fixture.currentHead)
	if paths, ok := tree["paths"].([]any); !ok || len(paths) < 2 {
		t.Fatalf("code/tree was item-capped instead of token-packed: %#v", tree)
	}
	treePagination := treePage.pagination
	if treePagination != nil {
		if treePagination["next_cursor"] == "" {
			t.Fatalf("code/tree page has continuation metadata without cursor: %#v", tree)
		}
		treePage = harness.callPage(t, "code/tree", map[string]any{"worktree": fixture.mainSelector, "cursor": treePagination["next_cursor"], "live": true})
		assertPublicCodeHead(t, treePage.result, fixture.currentHead)
	}

	searchPage := harness.callPage(t, "code/search", map[string]any{"worktree": fixture.mainSelector, "query": "needle", "live": true})
	search := searchPage.result
	assertPublicCodeHead(t, search, fixture.currentHead)
	if matches, ok := search["matches"].([]any); !ok || len(matches) < 2 {
		t.Fatalf("code/search was item-capped instead of token-packed: %#v", search)
	}
	searchPagination := searchPage.pagination
	if searchPagination != nil {
		if searchPagination["next_cursor"] == "" {
			t.Fatalf("repository-level code/search page has continuation metadata without cursor: %#v", search)
		}
		searchPage = harness.callPage(t, "code/search", map[string]any{"worktree": fixture.mainSelector, "query": "needle", "cursor": searchPagination["next_cursor"], "live": true})
		assertPublicCodeHead(t, searchPage.result, fixture.currentHead)
		firstMatches := search["matches"].([]any)
		secondMatches := searchPage.result["matches"].([]any)
		if len(firstMatches) == 0 || len(secondMatches) == 0 {
			t.Fatalf("code/search continuation was empty: first=%#v second=%#v", search, searchPage)
		}
		firstMatch := firstMatches[0].(map[string]any)
		secondMatch := secondMatches[0].(map[string]any)
		if firstMatch["path"] == secondMatch["path"] && firstMatch["line"] == secondMatch["line"] {
			t.Fatalf("code/search continuation repeated a match: first=%#v second=%#v", search, searchPage)
		}
	}

	readPage := harness.callPage(t, "code/read", map[string]any{"worktree": fixture.mainSelector, "path": "tracked.txt", "live": true})
	read := readPage.result
	assertPublicCodeReadHead(t, read, fixture.currentHead)
	readPagination := readPage.pagination
	if readPagination == nil || readPagination["next_cursor"] == "" {
		t.Fatalf("code/read first page is not paginated: %#v", read)
	}
	readPage = harness.callPage(t, "code/read", map[string]any{"worktree": fixture.mainSelector, "path": "tracked.txt", "cursor": readPagination["next_cursor"], "live": true})
	assertPublicCodeReadHead(t, readPage.result, fixture.currentHead)
	if readPage.result["start_line"].(float64) <= read["start_line"].(float64) {
		t.Fatalf("code/read continuation did not advance: %#v", readPage)
	}
	if readPage.pagination != nil {
		t.Fatalf("terminal code/read page exposed _pagination: %#v", readPage)
	}

	diffLine := strings.Repeat("x", 200) + "\n"
	if err := os.WriteFile(filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "diff-large.txt"), []byte(strings.Repeat(diffLine, 512)), 0o600); err != nil {
		t.Fatal(err)
	}
	diffPage := harness.callPage(t, "code/diff", map[string]any{"worktree": fixture.mainSelector, "paths": []any{"diff-large.txt"}, "live": true})
	diff := diffPage.result
	assertPublicCodeHead(t, diff, fixture.currentHead)
	diffText, ok := diff["diff"].(string)
	if !ok {
		t.Fatalf("code/diff returned no diff text: %#v", diff)
	}
	if len(diffText) <= 6000 {
		t.Fatalf("code/diff page appears driven by the internal 6KB candidate bound: bytes=%d", len(diffText))
	}
	diffPagination := diffPage.pagination
	if diffPagination == nil || diffPagination["next_cursor"] == "" {
		t.Fatalf("code/diff first page is not paginated: %#v", diff)
	}
	diffPage = harness.callPage(t, "code/diff", map[string]any{"worktree": fixture.mainSelector, "paths": []any{"diff-large.txt"}, "cursor": diffPagination["next_cursor"], "live": true})
	assertPublicCodeHead(t, diffPage.result, fixture.currentHead)
	if diffPage.result["diff"] == "" {
		t.Fatalf("code/diff continuation was empty: %#v", diffPage)
	}
	for action, input := range map[string]map[string]any{
		"code/worktree": {},
		"code/tree":     {"worktree": fixture.mainSelector, "live": true},
		"code/search":   {"worktree": fixture.mainSelector, "query": "needle", "live": true},
		"code/read":     {"worktree": fixture.mainSelector, "path": "tracked.txt", "live": true},
		"code/diff":     {"worktree": fixture.mainSelector, "paths": []any{"diff-large.txt"}, "live": true},
	} {
		harness.call(t, action, input)
	}

	var oversized strings.Builder
	for line := 0; line < 512; line++ {
		oversized.WriteString(strings.Repeat("oversized-token ", 40))
		oversized.WriteByte('\n')
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fixture.server.Service.Config.Projects["example"].Root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("oversized.txt", oversized.String())
	overflowPage := harness.callPage(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "oversized.txt", "live": true,
	})
	overflowPagination := overflowPage.pagination
	if overflowPagination == nil || overflowPagination["next_cursor"] == "" {
		t.Fatalf("oversized code/read did not auto-pack into continuation pages: %#v", overflowPage)
	}
}

func TestPublicCodeSearchContextLinesE2E(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)
	contextPath := filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "context-e2e.txt")
	if err := os.WriteFile(contextPath, []byte("needle-first\nneedle-adjacent\nmiddle\nneedle-last\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(contextPath) })
	zero := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "paths": []any{"context-e2e.txt"}, "query": "needle", "context_lines": 0, "live": true,
	})
	assertPublicCodeHead(t, zero, fixture.currentHead)
	if len(zero["matches"].([]any)) != 3 || zero["matches"].([]any)[0].(map[string]any)["snippet"] != "needle-first" {
		t.Fatalf("public context_lines=0/boundary result: %#v", zero)
	}
	result := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "paths": []any{"context-e2e.txt"}, "query": "needle", "context_lines": 1, "live": true,
	})
	assertPublicCodeHead(t, result, fixture.currentHead)
	matches, ok := result["matches"].([]any)
	if !ok || len(matches) == 0 {
		t.Fatalf("context search returned no matches: %#v", result)
	}
	first, ok := matches[0].(map[string]any)
	if !ok || first["snippet"] != "needle-first\nneedle-adjacent" || len(first["snippet"].(string)) > 240 {
		t.Fatalf("context search did not include surrounding lines: %#v", first)
	}
	last, ok := matches[2].(map[string]any)
	if !ok || last["snippet"] != "middle\nneedle-last" || len(last["snippet"].(string)) > 240 {
		t.Fatalf("context search did not preserve ending boundary: %#v", last)
	}
	widePage := harness.callPage(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "paths": []any{"tracked.txt"}, "query": "needle", "context_lines": 1, "live": true,
	})
	wide := widePage.result
	firstPagination := widePage.pagination
	if firstPagination == nil || firstPagination["next_cursor"] == "" {
		t.Fatalf("context search did not paginate: %#v", wide)
	}
	seen := make(map[int]bool)
	page := widePage
	for pageNumber := 0; ; pageNumber++ {
		assertPublicCodeHead(t, page.result, fixture.currentHead)
		matches, ok := page.result["matches"].([]any)
		if !ok || len(matches) == 0 {
			t.Fatalf("context pagination page %d has no matches: %#v", pageNumber, page)
		}
		for _, raw := range matches {
			match := raw.(map[string]any)
			line := int(match["line"].(float64))
			if line < 1 || line > 512 || seen[line] {
				t.Fatalf("context pagination duplicate/invalid line %d on page %d: %#v", line, pageNumber, page)
			}
			seen[line] = true
		}
		pagination := page.pagination
		if pagination == nil {
			break
		}
		if pageNumber > 100 || pagination["next_cursor"] == "" {
			t.Fatalf("context pagination did not terminate safely: %#v", page)
		}
		page = harness.callPage(t, "code/search", map[string]any{
			"worktree": fixture.mainSelector, "paths": []any{"tracked.txt"}, "query": "needle", "context_lines": 1, "cursor": pagination["next_cursor"], "live": true,
		})
	}
	if len(seen) != 512 {
		t.Fatalf("context pagination skipped matches: got %d want 512", len(seen))
	}
	wrongScope := harness.client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{
			"session": harness.sessionID, "action": "code/search", "input": map[string]any{
				"worktree": fixture.mainSelector, "paths": []any{"tracked.txt"}, "query": "needle", "context_lines": 2, "cursor": firstPagination["next_cursor"], "live": true,
			},
		},
	})
	if genericStructured(t, wrongScope)["is_error"] != true {
		t.Fatalf("context cursor with different context_lines was accepted: %#v", wrongScope)
	}
}
