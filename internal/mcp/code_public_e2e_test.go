package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
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
	server    *Server
	sessionID string
	counter   *tokenizer.Counter
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

	trainPath := filepath.Join(stateDir, "work", "EXM", "TRN1")
	if err := os.MkdirAll(filepath.Dir(trainPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "branch", "train/EXM-TRN1", currentHead)
	t.Cleanup(func() {
		testutil.Git(t, root, "worktree", "remove", "--force", trainPath)
		testutil.Git(t, root, "branch", "-D", "train/EXM-TRN1")
	})
	testutil.Git(t, root, "worktree", "add", trainPath, "train/EXM-TRN1")

	now := time.Now().UTC()
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion, ID: "EXM-TRN1", ProjectID: "example", Revision: 1,
		Status: model.TrainV2Planned, CreatedBy: "performance-test", CreatedAt: now, UpdatedAt: now,
		Items: []model.TrainV2Item{{Position: 0, TaskID: "EXM-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemQueued, AddedAt: now}},
	}
	payload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{
		OperationID: "OPR-EXM-CODE-E2E", EntityType: "train", EntityID: train.ID, Revision: 1,
		Kind: "code-e2e-fixture", Payload: payload, Create: true,
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
	counter := tokenizer.NewCounter()
	if _, err := counter.CountText([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	return publicCodeCallHarness{server: fixture.server, sessionID: fixture.sessionID, counter: counter}
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
	body := mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": h.sessionID, "action": action, "input": input,
		}},
	})
	started := time.Now()
	response := callMCPRaw(t, h.server, body)
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

	if err := os.WriteFile(filepath.Join(fixture.server.Service.Config.Projects["example"].Root, "diff-large.txt"), []byte(strings.Repeat("x\n", 512)), 0o600); err != nil {
		t.Fatal(err)
	}
	diff := harness.call(t, "code/diff", map[string]any{"worktree": fixture.mainSelector, "paths": []any{"diff-large.txt"}, "live": true})
	assertPublicCodeHead(t, diff, fixture.currentHead)
	diffText, ok := diff["diff"].(string)
	if !ok {
		t.Fatalf("code/diff returned no diff text: %#v", diff)
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
	if strings.Count(diffText, "+x\n") <= 128 {
		t.Fatalf("code/diff appears to use a hidden 128-line page driver: first_added_lines=%d", strings.Count(diffText, "+x\n"))
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
