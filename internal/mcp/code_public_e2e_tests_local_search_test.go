package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicCodeSearchAndDiffOverflowE2ELocalSetup(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)

	searchPage := harness.callPage(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": "needle", "live": true,
	})
	search := searchPage.result
	assertPublicCodeHead(t, search, fixture.currentHead)
	if len(search["matches"].([]any)) == 0 || searchPage.pagination == nil {
		t.Fatalf("overflow search did not paginate: %#v", search)
	}

	diffLine := strings.Repeat("overflow-diff ", 160) + "\n"
	diffPath := filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "diff-overflow.txt")
	if err := os.WriteFile(diffPath, []byte(strings.Repeat(diffLine, 120)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(diffPath) })
	diffPage := harness.callPage(t, "code/diff", map[string]any{
		"worktree": fixture.mainSelector, "paths": []any{"diff-overflow.txt"}, "live": true,
	})
	diff := diffPage.result
	assertPublicCodeHead(t, diff, fixture.currentHead)
	if diff["diff"] == "" || diffPage.pagination == nil {
		t.Fatalf("overflow diff did not paginate: %#v", diff)
	}
}

func TestPublicCodeActionsFitPublicEnvelopeE2ELocalSetup(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)

	inputs := map[string]map[string]any{
		"code/worktree": {},
		"code/tree":     {"worktree": fixture.mainSelector, "live": true},
		"code/search":   {"worktree": fixture.mainSelector, "query": "needle", "live": true},
		"code/read":     {"worktree": fixture.mainSelector, "path": "tracked.txt", "live": true},
	}
	for _, action := range []string{"code/worktree", "code/tree", "code/search", "code/read"} {
		result := harness.call(t, action, inputs[action])
		if action == "code/read" {
			assertPublicCodeReadHead(t, result, fixture.currentHead)
		} else if action != "code/worktree" {
			assertPublicCodeHead(t, result, fixture.currentHead)
		}
	}

	diffPath := filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "all-actions-diff.txt")
	if err := os.WriteFile(diffPath, []byte(strings.Repeat(strings.Repeat("envelope-diff ", 160)+"\n", 120)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(diffPath) })
	diff := harness.call(t, "code/diff", map[string]any{
		"worktree": fixture.mainSelector, "paths": []any{"all-actions-diff.txt"}, "live": true,
	})
	assertPublicCodeHead(t, diff, fixture.currentHead)
}

func TestPublicCodeReadExactBoundedRangeE2E(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)
	immutablePage := harness.callPage(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "tracked.txt", "start_line": 2, "line_count": 1,
	})
	immutable := immutablePage.result
	assertPublicCodeReadHead(t, immutable, fixture.currentHead)
	content := strings.Repeat("needle tracked line\n", 512)
	digest := sha256.Sum256([]byte(content))
	wantHash := hex.EncodeToString(digest[:])[:8]
	if immutable["file_hash"] != wantHash {
		t.Fatalf("public code/read file_hash=%#v want SHA256/8 %q", immutable["file_hash"], wantHash)
	}
	if immutable["start_line"] != float64(2) || immutable["end_line"] != float64(2) || immutable["content"] != "needle tracked line" || immutablePage.pagination != nil {
		t.Fatalf("public live=false bounded read was not immutable and exact: %#v", immutable)
	}
	rangePath := filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "range-e2e.txt")
	if err := os.WriteFile(rangePath, []byte(strings.Join([]string{
		"range-01", "range-02", "range-03", "range-04", "range-05", "range-06", "range-07", "range-08",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(rangePath) })

	shortPage := harness.callPage(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "range-e2e.txt", "start_line": 3, "line_count": 2, "live": true,
	})
	short := shortPage.result
	assertPublicCodeReadHead(t, short, fixture.currentHead)
	if short["start_line"] != float64(3) || short["end_line"] != float64(4) || short["total_lines"] != float64(8) || short["content"] != "range-03\nrange-04" || shortPage.pagination != nil {
		t.Fatalf("public exact range was not bounded: %#v", short)
	}

	nearEOFPage := harness.callPage(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "range-e2e.txt", "start_line": 7, "line_count": 5, "live": true,
	})
	nearEOF := nearEOFPage.result
	if nearEOF["start_line"] != float64(7) || nearEOF["end_line"] != float64(8) || nearEOF["content"] != "range-07\nrange-08" || nearEOFPage.pagination != nil {
		t.Fatalf("public near-EOF range was not clamped: %#v", nearEOF)
	}

	var wide strings.Builder
	for line := 1; line <= 120; line++ {
		wide.WriteString(strings.Repeat("range-token ", 40))
		wide.WriteByte('\n')
	}
	widePath := filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "wide-range-e2e.txt")
	if err := os.WriteFile(widePath, []byte(wide.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(widePath) })
	page := harness.callPage(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "wide-range-e2e.txt", "start_line": 10, "line_count": 100, "live": true,
	})
	if page.result["start_line"] != float64(10) || page.pagination == nil {
		t.Fatalf("public oversized range did not return a continuation: %#v", page)
	}
	wantStart := 10
	for pages := 0; ; pages++ {
		if page.result["start_line"] != float64(wantStart) || page.result["end_line"].(float64) > 109 {
			t.Fatalf("public range continuation was not exact: %#v", page)
		}
		pagination := page.pagination
		if pagination == nil {
			if page.result["end_line"] != float64(109) {
				t.Fatalf("public range continuation ended at %#v, want 109", page.result["end_line"])
			}
			break
		}
		if pages > 100 || pagination["next_cursor"] == "" {
			t.Fatalf("public range continuation did not remain bounded: %#v", page)
		}
		wantStart = int(page.result["end_line"].(float64)) + 1
		page = harness.callPage(t, "code/read", map[string]any{
			"worktree": fixture.mainSelector, "path": "wide-range-e2e.txt", "cursor": pagination["next_cursor"], "live": true,
		})
	}

	for _, lineCount := range []int{0, -1} {
		response := harness.client.request(t, "tools/call", map[string]any{
			"name": "call", "arguments": map[string]any{
				"session": harness.sessionID, "action": "code/read", "input": map[string]any{
					"worktree": fixture.mainSelector, "path": "range-e2e.txt", "line_count": lineCount, "live": true,
				},
			},
		})
		structured := genericStructured(t, response)
		if structured["is_error"] != true {
			t.Fatalf("public invalid line_count %d was accepted: %#v", lineCount, structured)
		}
	}
}
