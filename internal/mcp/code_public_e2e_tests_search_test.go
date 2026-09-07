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

	worktree := harness.call(t, "code/worktree", map[string]any{})
	items, ok := worktree["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("code/worktree first page=%#v", worktree)
	}
	firstItem := items[0].(map[string]any)
	worktreePagination := publicPagination(t, worktree)
	if firstItem["head"] != fixture.currentHead {
		t.Fatalf("code/worktree first page lacks exact head: %#v", worktree)
	}
	if worktreePagination != nil {
		t.Fatalf("code/worktree was item-capped instead of token-packed: %#v", worktree)
	}

	tree := harness.call(t, "code/tree", map[string]any{"worktree": fixture.mainSelector, "live": true})
	assertPublicCodeHead(t, tree, fixture.currentHead)
	if paths, ok := tree["paths"].([]any); !ok || len(paths) < 2 {
		t.Fatalf("code/tree was item-capped instead of token-packed: %#v", tree)
	}
	treePagination := publicPagination(t, tree)
	if treePagination != nil {
		if treePagination["next_cursor"] == "" {
			t.Fatalf("code/tree page has continuation metadata without cursor: %#v", tree)
		}
		treePage := harness.call(t, "code/tree", map[string]any{"worktree": fixture.mainSelector, "cursor": treePagination["next_cursor"], "live": true})
		assertPublicCodeHead(t, treePage, fixture.currentHead)
	}

	search := harness.call(t, "code/search", map[string]any{"worktree": fixture.mainSelector, "query": "needle", "live": true})
	assertPublicCodeHead(t, search, fixture.currentHead)
	if matches, ok := search["matches"].([]any); !ok || len(matches) < 2 {
		t.Fatalf("code/search was item-capped instead of token-packed: %#v", search)
	}
	searchPagination := publicPagination(t, search)
	if searchPagination != nil {
		if searchPagination["next_cursor"] == "" {
			t.Fatalf("repository-level code/search page has continuation metadata without cursor: %#v", search)
		}
		searchPage := harness.call(t, "code/search", map[string]any{"worktree": fixture.mainSelector, "query": "needle", "cursor": searchPagination["next_cursor"], "live": true})
		assertPublicCodeHead(t, searchPage, fixture.currentHead)
		firstMatches := search["matches"].([]any)
		secondMatches := searchPage["matches"].([]any)
		if len(firstMatches) == 0 || len(secondMatches) == 0 {
			t.Fatalf("code/search continuation was empty: first=%#v second=%#v", search, searchPage)
		}
		firstMatch := firstMatches[0].(map[string]any)
		secondMatch := secondMatches[0].(map[string]any)
		if firstMatch["path"] == secondMatch["path"] && firstMatch["line"] == secondMatch["line"] {
			t.Fatalf("code/search continuation repeated a match: first=%#v second=%#v", search, searchPage)
		}
	}

	read := harness.call(t, "code/read", map[string]any{"worktree": fixture.mainSelector, "path": "tracked.txt", "live": true})
	assertPublicCodeReadHead(t, read, fixture.currentHead)
	readPagination := publicPagination(t, read)
	if readPagination == nil || readPagination["next_cursor"] == "" {
		t.Fatalf("code/read first page is not paginated: %#v", read)
	}
	readPage := harness.call(t, "code/read", map[string]any{"worktree": fixture.mainSelector, "path": "tracked.txt", "cursor": readPagination["next_cursor"], "live": true})
	assertPublicCodeReadHead(t, readPage, fixture.currentHead)
	if readPage["start_line"].(float64) <= read["start_line"].(float64) {
		t.Fatalf("code/read continuation did not advance: %#v", readPage)
	}
	if _, ok := readPage["_pagination"]; ok {
		t.Fatalf("terminal code/read page exposed _pagination: %#v", readPage)
	}

	diffLine := strings.Repeat("x", 200) + "\n"
	if err := os.WriteFile(filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "diff-large.txt"), []byte(strings.Repeat(diffLine, 512)), 0o600); err != nil {
		t.Fatal(err)
	}
	diff := harness.call(t, "code/diff", map[string]any{"worktree": fixture.mainSelector, "paths": []any{"diff-large.txt"}, "live": true})
	assertPublicCodeHead(t, diff, fixture.currentHead)
	diffText, ok := diff["diff"].(string)
	if !ok {
		t.Fatalf("code/diff returned no diff text: %#v", diff)
	}
	if len(diffText) <= 6000 {
		t.Fatalf("code/diff page appears driven by the internal 6KB candidate bound: bytes=%d", len(diffText))
	}
	diffPagination := publicPagination(t, diff)
	if diffPagination == nil || diffPagination["next_cursor"] == "" {
		t.Fatalf("code/diff first page is not paginated: %#v", diff)
	}
	diffPage := harness.call(t, "code/diff", map[string]any{"worktree": fixture.mainSelector, "paths": []any{"diff-large.txt"}, "cursor": diffPagination["next_cursor"], "live": true})
	assertPublicCodeHead(t, diffPage, fixture.currentHead)
	if diffPage["diff"] == "" {
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
	overflow := harness.call(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "oversized.txt", "live": true,
	})
	overflowPagination := publicPagination(t, overflow)
	if overflowPagination == nil || overflowPagination["next_cursor"] == "" {
		t.Fatalf("oversized code/read did not auto-pack into continuation pages: %#v", overflow)
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
	wide := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "paths": []any{"tracked.txt"}, "query": "needle", "context_lines": 1, "live": true,
	})
	firstPagination := publicPagination(t, wide)
	if firstPagination == nil || firstPagination["next_cursor"] == "" {
		t.Fatalf("context search did not paginate: %#v", wide)
	}
	seen := make(map[int]bool)
	page := wide
	for pageNumber := 0; ; pageNumber++ {
		assertPublicCodeHead(t, page, fixture.currentHead)
		matches, ok := page["matches"].([]any)
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
		pagination := publicPagination(t, page)
		if pagination == nil {
			break
		}
		if pageNumber > 100 || pagination["next_cursor"] == "" {
			t.Fatalf("context pagination did not terminate safely: %#v", page)
		}
		page = harness.call(t, "code/search", map[string]any{
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
