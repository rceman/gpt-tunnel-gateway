package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

const publicCodeCallLimit = time.Second

type publicCodeE2EFixture struct {
	server       *Server
	sessionID    string
	mainSelector string
	currentHead  string
}

type publicCodeCallHarness struct {
	sessionID string
	counter   *tokenizer.Counter
	client    *frozenConnectorClient
}

func newPublicCodeE2EFixture(t *testing.T) publicCodeE2EFixture {
	t.Helper()
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "code inspection performance fixture\n")
	write("new.txt", "needle in a new file\n")
	var content strings.Builder
	for line := 0; line < 512; line++ {
		content.WriteString("needle tracked line\n")
	}
	write("tracked.txt", content.String())
	testutil.Git(t, root, "add", ".")
	testutil.Git(t, root, "commit", "-m", "code inspection public fixture")
	currentHead := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD"))
	testutil.Git(t, root, "push", "origin", "main")

	stateDir := t.TempDir()
	project := config.ProjectConfig{Root: root, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "code-e2e-agent"}
	c := config.Config{
		SchemaVersion: 1, GatewayID: "code-e2e-gateway", StateDir: stateDir,
		MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1,
		Hub:      config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"},
		Projects: map[string]config.ProjectConfig{"example": project},
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := service.NewWithDurabilityDeferredWorkers(c, db)
	if _, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{
		Project: model.Project{
			SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git",
			DefaultBranch: "main", WorkflowRepository: "planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active",
		},
		WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead},
	}); err != nil {
		t.Fatal(err)
	}

	hotfixPath := filepath.Join(stateDir, "hotfix-worktrees", "example", "fixture")
	if err := os.MkdirAll(filepath.Dir(hotfixPath), 0o700); err != nil {
		t.Fatal(err)
	}
	branch := "hotfix/fixture"
	testutil.Git(t, root, "branch", branch, currentHead)
	t.Cleanup(func() {
		testutil.Git(t, root, "worktree", "remove", "--force", hotfixPath)
		testutil.Git(t, root, "branch", "-D", branch)
	})
	testutil.Git(t, root, "worktree", "add", hotfixPath, branch)
	if err := os.WriteFile(filepath.Join(hotfixPath, "fixture-hotfix.txt"), []byte("fixture hotfix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, hotfixPath, "add", "fixture-hotfix.txt")
	testutil.Git(t, hotfixPath, "commit", "-m", "code inspection hotfix fixture")
	if err := s.Git.RecordHotfixIdentity(stateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: currentHead, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	store := durableSession.NewStore(stateDir)
	session, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	return publicCodeE2EFixture{
		server:       &Server{Service: s, AuthorityContext: authority.WithPlanner(context.Background())},
		sessionID:    session.ID,
		mainSelector: "WT-MAIN-" + currentHead[:8],
		currentHead:  currentHead,
	}
}

func newPublicCodeCallHarness(t *testing.T, fixture publicCodeE2EFixture) publicCodeCallHarness {
	t.Helper()
	httpServer := httptest.NewServer(fixture.server.Router())
	t.Cleanup(httpServer.Close)
	counter := tokenizer.NewCounter()
	if _, err := counter.CountText([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	return publicCodeCallHarness{
		sessionID: fixture.sessionID, counter: counter,
		client: &frozenConnectorClient{http: httpServer.Client(), endpoint: httpServer.URL + "/mcp", methods: map[string]int{}},
	}
}

func (h publicCodeCallHarness) call(t *testing.T, action string, input map[string]any) map[string]any {
	t.Helper()
	response, _, _ := h.callResponse(t, action, input)
	result := genericActionResult(t, response)
	assertPublicCodePagination(t, result)
	return result
}

func (h publicCodeCallHarness) callResponse(t *testing.T, action string, input map[string]any) (map[string]any, time.Duration, int) {
	t.Helper()
	started := time.Now()
	response := h.client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{
			"session": h.sessionID, "action": action, "input": input,
		},
	})
	elapsed := time.Since(started)
	if elapsed >= publicCodeCallLimit {
		t.Fatalf("%s exceeded %s: %s", action, publicCodeCallLimit, elapsed)
	}
	serialized, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := h.counter.CountText(serialized)
	if err != nil {
		t.Fatalf("%s output token count failed: %v", action, err)
	}
	if tokens > tokenizer.MaxTokens {
		t.Fatalf("%s output exceeded %d tokens: %d", action, tokenizer.MaxTokens, tokens)
	}
	t.Logf("%s: elapsed_ms=%d output_tokens=%d", action, elapsed.Milliseconds(), tokens)
	return response, elapsed, tokens
}

func assertPublicCodeHead(t *testing.T, result map[string]any, want string) {
	t.Helper()
	if result["head"] != want {
		t.Fatalf("public code result head=%#v want %q: %#v", result["head"], want, result)
	}
}

func publicPagination(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	pagination, ok := result["_pagination"].(map[string]any)
	if !ok {
		return nil
	}
	return pagination
}

func assertPublicCodePagination(t *testing.T, result map[string]any) {
	t.Helper()
	for _, field := range []string{"next_cursor", "truncated"} {
		if _, ok := result[field]; ok {
			t.Fatalf("public code result exposes legacy pagination field %q: %#v", field, result)
		}
	}
	if pagination, ok := result["_pagination"].(map[string]any); ok {
		if len(pagination) != 1 {
			t.Fatalf("public code _pagination has unexpected fields: %#v", pagination)
		}
		if _, ok := pagination["next_cursor"].(string); !ok {
			t.Fatalf("public code _pagination lacks next_cursor: %#v", pagination)
		}
	}
}

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
	assertPublicCodeHead(t, read, fixture.currentHead)
	readPagination := publicPagination(t, read)
	if readPagination == nil || readPagination["next_cursor"] == "" {
		t.Fatalf("code/read first page is not paginated: %#v", read)
	}
	readPage := harness.call(t, "code/read", map[string]any{"worktree": fixture.mainSelector, "path": "tracked.txt", "cursor": readPagination["next_cursor"], "live": true})
	assertPublicCodeHead(t, readPage, fixture.currentHead)
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

func TestPublicCodeSearchAndDiffOverflowE2ELocalSetup(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)

	search := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": "needle", "live": true,
	})
	assertPublicCodeHead(t, search, fixture.currentHead)
	if len(search["matches"].([]any)) == 0 || publicPagination(t, search) == nil {
		t.Fatalf("overflow search did not paginate: %#v", search)
	}

	diffLine := strings.Repeat("overflow-diff ", 160) + "\n"
	diffPath := filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "diff-overflow.txt")
	if err := os.WriteFile(diffPath, []byte(strings.Repeat(diffLine, 120)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(diffPath) })
	diff := harness.call(t, "code/diff", map[string]any{
		"worktree": fixture.mainSelector, "paths": []any{"diff-overflow.txt"}, "live": true,
	})
	assertPublicCodeHead(t, diff, fixture.currentHead)
	if diff["diff"] == "" || publicPagination(t, diff) == nil {
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
		if action != "code/worktree" {
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
func TestPublicCodeReadExactBoundedRangeE2E(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)
	rangePath := filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "range-e2e.txt")
	if err := os.WriteFile(rangePath, []byte(strings.Join([]string{
		"range-01", "range-02", "range-03", "range-04", "range-05", "range-06", "range-07", "range-08",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(rangePath) })

	short := harness.call(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "range-e2e.txt", "start_line": 3, "line_count": 2, "live": true,
	})
	assertPublicCodeHead(t, short, fixture.currentHead)
	if short["start_line"] != float64(3) || short["end_line"] != float64(4) || short["total_lines"] != float64(8) || short["content"] != "range-03\nrange-04" || publicPagination(t, short) != nil {
		t.Fatalf("public exact range was not bounded: %#v", short)
	}

	nearEOF := harness.call(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "range-e2e.txt", "start_line": 7, "line_count": 5, "live": true,
	})
	if nearEOF["start_line"] != float64(7) || nearEOF["end_line"] != float64(8) || nearEOF["content"] != "range-07\nrange-08" || publicPagination(t, nearEOF) != nil {
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
	page := harness.call(t, "code/read", map[string]any{
		"worktree": fixture.mainSelector, "path": "wide-range-e2e.txt", "start_line": 10, "line_count": 100, "live": true,
	})
	if page["start_line"] != float64(10) || publicPagination(t, page) == nil {
		t.Fatalf("public oversized range did not return a continuation: %#v", page)
	}
	wantStart := 10
	for pages := 0; ; pages++ {
		if page["start_line"] != float64(wantStart) || page["end_line"].(float64) > 109 {
			t.Fatalf("public range continuation was not exact: %#v", page)
		}
		pagination := publicPagination(t, page)
		if pagination == nil {
			if page["end_line"] != float64(109) {
				t.Fatalf("public range continuation ended at %#v, want 109", page["end_line"])
			}
			break
		}
		if pages > 100 || pagination["next_cursor"] == "" {
			t.Fatalf("public range continuation did not remain bounded: %#v", page)
		}
		wantStart = int(page["end_line"].(float64)) + 1
		page = harness.call(t, "code/read", map[string]any{
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
