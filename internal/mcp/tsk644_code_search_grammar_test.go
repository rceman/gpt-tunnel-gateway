package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK644CodeSearchSchemaExposesLiteralAlternativesAndCaseMode(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		GatewayID: "tsk644-schema-test", StateDir: t.TempDir(),
	}, nil)}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["code/search"]
	if !ok {
		t.Fatal("code/search is not registered")
	}
	if entry.InputSchema["additionalProperties"] != false {
		t.Fatalf("code/search input is not closed: %#v", entry.InputSchema)
	}
	properties := schemaProperties(entry.InputSchema)
	caseInsensitive, ok := properties["case_insensitive"].(map[string]any)
	if !ok || caseInsensitive["type"] != "boolean" || caseInsensitive["default"] != false {
		t.Fatalf("code/search case_insensitive schema=%#v", properties["case_insensitive"])
	}
	query, ok := properties["query"].(map[string]any)
	if !ok {
		t.Fatalf("code/search query schema=%#v", properties["query"])
	}
	description, _ := query["description"].(string)
	for _, want := range []string{"alternatives", `\|`, `\\`, "regular expressions"} {
		if !strings.Contains(description, want) {
			t.Fatalf("code/search query description omitted %q: %q", want, description)
		}
	}
	required := stringList(entry.InputSchema["required"])
	if len(required) != 2 || required[0] != "worktree" || required[1] != "query" {
		t.Fatalf("code/search required=%v", required)
	}
	for _, field := range required {
		if field == "case_insensitive" {
			t.Fatal("code/search case_insensitive must remain optional")
		}
	}
	if !strings.Contains(entry.Description, "literal alternatives") || !strings.Contains(entry.Description, "case-insensitive") {
		t.Fatalf("code/search description omitted the literal grammar: %q", entry.Description)
	}
}

func tsk644WriteFixtureFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

func tsk644SearchMatchPaths(t *testing.T, result map[string]any) []string {
	t.Helper()
	matches, ok := result["matches"].([]any)
	if !ok {
		t.Fatalf("code/search matches=%#v", result["matches"])
	}
	paths := make([]string, 0, len(matches))
	for _, raw := range matches {
		match, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("code/search match=%#v", raw)
		}
		path, _ := match["path"].(string)
		paths = append(paths, path)
	}
	return paths
}

func tsk644SearchError(t *testing.T, harness publicCodeCallHarness, input map[string]any) string {
	t.Helper()
	response := harness.client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{
			"session": harness.sessionID, "action": "code/search", "input": input,
		},
	})
	structured := genericStructured(t, response)
	if structured["is_error"] != true {
		t.Fatalf("code/search accepted malformed input: %#v", structured)
	}
	result, _ := structured["result"].(map[string]any)
	errorValue, _ := result["error"].(map[string]any)
	message, _ := errorValue["message"].(string)
	return message
}

func TestTSK644PublicCodeSearchLiteralGrammarE2E(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)
	root := fixture.server.Service.Config.Projects["example"].Root
	tsk644WriteFixtureFile(t, root, "tsk644-alpha.txt", "alpha-only-marker\n")
	tsk644WriteFixtureFile(t, root, "tsk644-beta.txt", "beta-only-marker\n")
	tsk644WriteFixtureFile(t, root, "tsk644-pipe.txt", "left | right\npath\\to\\file\n")
	tsk644WriteFixtureFile(t, root, "tsk644-case.txt", "MixedCaseMarker\n")
	paths := []any{"tsk644-alpha.txt", "tsk644-beta.txt"}

	alternatives := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": "alpha-only-marker|beta-only-marker", "paths": paths, "live": true,
	})
	if got := tsk644SearchMatchPaths(t, alternatives); len(got) != 2 || got[0] != "tsk644-alpha.txt" || got[1] != "tsk644-beta.txt" {
		t.Fatalf("literal alternatives matches=%v", got)
	}

	escapedPipe := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": `left \| right`, "paths": []any{"tsk644-pipe.txt"}, "live": true,
	})
	if got := tsk644SearchMatchPaths(t, escapedPipe); len(got) != 1 {
		t.Fatalf("escaped pipe matches=%v", got)
	}
	escapedBackslash := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": `path\\to\\file`, "paths": []any{"tsk644-pipe.txt"}, "live": true,
	})
	if got := tsk644SearchMatchPaths(t, escapedBackslash); len(got) != 1 {
		t.Fatalf("escaped backslash matches=%v", got)
	}

	sensitive := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": "mixedcasemarker", "paths": []any{"tsk644-case.txt"}, "live": true,
	})
	if got := tsk644SearchMatchPaths(t, sensitive); len(got) != 0 {
		t.Fatalf("default search matched without case: %v", got)
	}
	insensitive := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": "mixedcasemarker", "paths": []any{"tsk644-case.txt"}, "case_insensitive": true, "live": true,
	})
	if got := tsk644SearchMatchPaths(t, insensitive); len(got) != 1 {
		t.Fatalf("case-insensitive search matches=%v", got)
	}
	everyAlternative := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": "alpha-only-marker|MIXEDCASEMARKER",
		"paths": []any{"tsk644-alpha.txt", "tsk644-case.txt"}, "case_insensitive": true, "live": true,
	})
	if got := tsk644SearchMatchPaths(t, everyAlternative); len(got) != 2 {
		t.Fatalf("case_insensitive did not apply to every alternative: %v", got)
	}

	filtered := harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector, "query": "alpha-only-marker|beta-only-marker", "paths": []any{"tsk644-alpha.txt"}, "live": true,
	})
	if got := tsk644SearchMatchPaths(t, filtered); len(got) != 1 || got[0] != "tsk644-alpha.txt" {
		t.Fatalf("path-filtered alternatives matches=%v", got)
	}

	for name, query := range map[string]string{
		"empty alternative":  "alpha||beta",
		"leading pipe":       "|alpha",
		"trailing pipe":      "alpha|",
		"trailing escape":    `alpha\`,
		"unsupported escape": `alpha\beta`,
	} {
		message := tsk644SearchError(t, harness, map[string]any{
			"worktree": fixture.mainSelector, "query": query, "paths": []any{"tsk644-alpha.txt"}, "live": true,
		})
		if !strings.Contains(message, "search query") {
			t.Fatalf("%s error=%q", name, message)
		}
	}
}
