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

	store := mcpSQLiteSessionStore(t, s)
	session, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	return publicCodeE2EFixture{
		server: &Server{
			Service:          s,
			AuthorityContext: authority.WithPlanner(context.Background()),
		},
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
		sessionID: fixture.sessionID,
		counter:   counter,
		client: &frozenConnectorClient{
			http:     httpServer.Client(),
			endpoint: httpServer.URL + "/mcp",
			methods:  map[string]int{},
		},
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

func assertPublicCodeReadHead(t *testing.T, result map[string]any, want string) {
	t.Helper()
	if result["head"] != want[:8] {
		t.Fatalf("public code/read head=%#v want %q: %#v", result["head"], want[:8], result)
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
