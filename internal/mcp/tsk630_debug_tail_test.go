package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK630DebugTailUsesDirectBoundedRecoverySelector(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + logPath + "\"\ni=1\nwhile [ \"$i\" -le 150 ]; do printf 'line-%s\\n' \"$i\"; i=$((i+1)); done\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := mcpServiceWithSQLite(t, config.Config{
		Debug:                  config.DebugConfig{Enabled: true},
		StateDir:               t.TempDir(),
		AirelayCommand:         script,
		DispatchTimeoutSeconds: 5,
	})
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	entry := server.genericActionRegistry(server.tools())["debug/tail"]
	if entry.Authority != nil || !entry.LocalReadOnly || !entry.LocalReceiptOnly || entry.SessionBound {
		t.Fatalf("debug/tail retained normal routing or write authority: %#v", entry)
	}
	if entry.AuthorityRole != durableSession.RolePlanner {
		t.Fatalf("debug/tail authority role=%q", entry.AuthorityRole)
	}
	store := mcpSQLiteSessionStore(t, server.Service)
	planner := debugTestSession(t, store, durableSession.RolePlanner)
	call := func(input map[string]any) map[string]any {
		t.Helper()
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session": planner.ID, "action": "debug/tail", "input": input,
			}},
		}))
		return typedStructured(t, response)
	}
	check := func(input map[string]any, requested int, first string) {
		t.Helper()
		structured := call(input)
		if structured["ok"] != true {
			t.Fatalf("debug/tail failed for %v: %#v", input, structured)
		}
		result := structured["result"].(map[string]any)
		if result["status"] != "ok" || result["airelay_session"] != "debug_session" || result["exit_code"] != float64(0) {
			t.Fatalf("debug/tail evidence=%#v", result)
		}
		lines, ok := result["lines"].([]any)
		if !ok || len(lines) != requested || lines[0] != first || lines[len(lines)-1] != "line-150" {
			t.Fatalf("debug/tail lines=%#v requested=%d", result["lines"], requested)
		}
		args, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		wantArgs := "tail\ndebug_session\n--lines\n" + strconv.Itoa(requested) + "\n"
		if string(args) != wantArgs {
			t.Fatalf("tail argv=%q want %q", args, wantArgs)
		}
	}
	check(map[string]any{"airelay_session": "debug_session"}, 20, "line-131")
	check(map[string]any{"airelay_session": "debug_session", "lines": 1}, 1, "line-150")
	check(map[string]any{"airelay_session": "debug_session", "lines": 100}, 100, "line-51")
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, lines := range []int{0, 101} {
		structured := call(map[string]any{"airelay_session": "debug_session", "lines": lines})
		if structured["ok"] != false {
			t.Fatalf("invalid debug/tail line count %d was accepted: %#v", lines, structured)
		}
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("invalid debug/tail bounds invoked Airelay: before=%q after=%q", before, after)
	}
}

func TestTSK630DebugTailPlannerOnlyBeforeLeadAuthority(t *testing.T) {
	fixture := newTSK571HTTPFixture(t, nil, true, true)
	fixture.server.Service.Config.Debug.Enabled = true
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	contents := "#!/bin/sh\nif [ \"$1\" = session-status ]; then printf 'Controller: reachable\\nState: idle\\n'; exit 0; fi\nprintf 'recovery line\\n'\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.server.Service.Airelay.Command = script
	fixture.server.Service.Airelay.Timeout = 5 * time.Second
	fixture.server.Service.Config.AirelayCommand = script

	runtimes := []struct {
		agent   string
		runtime string
		role    string
	}{
		{agent: "coding-planner", runtime: "runtime-tsk630-planner", role: durableSession.RolePlanner},
		{agent: "coding-lead", runtime: "runtime-tsk630-lead", role: durableSession.RoleLead},
		{agent: "coding-advisor", runtime: "runtime-tsk630-advisor", role: durableSession.RoleAdvisor},
		{agent: "coding-worker", runtime: "runtime-tsk630-worker", role: durableSession.RoleWorker},
	}
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID] = map[string]config.AgentBinding{}
	revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range runtimes {
		revision = seedTSK571Agent(t, fixture.server.Service, revision, item.agent, true)
		fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID][item.agent] = config.AgentBinding{SessionKey: item.runtime, Profile: "coding"}
		fixture.addSession(t, fixture.projectID, "EXM", item.role, item.runtime)
	}
	for _, item := range runtimes {
		if _, err := fixture.server.Service.ResolveRuntimeRoleSession(context.Background(), item.runtime, item.role); err != nil {
			t.Fatalf("runtime identity %s/%s was not valid: %v", item.role, item.runtime, err)
		}
		result := fixture.call(t, item.runtime, "debug/tail", map[string]any{"airelay_session": "debug_session", "lines": 1})
		allowed := item.role == durableSession.RolePlanner
		if (result["ok"] == true) != allowed {
			t.Fatalf("debug/tail role=%s runtime=%s allowed=%v result=%#v", item.role, item.runtime, allowed, result)
		}
	}
}
