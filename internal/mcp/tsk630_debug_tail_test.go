package mcp

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK630DebugTailUsesDirectAgentRefAndBoundedRecoverySelector(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + logPath + "\"\ni=1\nwhile [ \"$i\" -le 150 ]; do printf 'line-%s\\n' \"$i\"; i=$((i+1)); done\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleWorker}, true, true)
	fixture.server.Service.Config.Debug.Enabled = true
	fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, "debug_session")
	fixture.server.Service.Airelay.Command = script
	fixture.server.Service.Config.AirelayCommand = script
	fixture.server.Service.Airelay.Timeout = 5 * time.Second
	server := fixture.server
	entry := server.genericActionRegistry(server.tools())["debug/tail"]
	if entry.Authority != nil || !entry.LocalReadOnly || !entry.LocalReceiptOnly || !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("debug/tail contract=%#v", entry)
	}
	if entry.AuthorityRole != actionRolePlannerOrLead {
		t.Fatalf("debug/tail authority role=%q", entry.AuthorityRole)
	}
	planner := fixture.sessions[durableSession.RolePlanner]
	call := func(input map[string]any) map[string]any {
		t.Helper()
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session": planner, "action": "debug/tail", "input": input,
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
		if result["status"] != "ok" || result["agent_ref"] != "debug_session" || result["exit_code"] != float64(0) {
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
	check(map[string]any{"agent_ref": "debug_session"}, 20, "line-131")
	check(map[string]any{"agent_ref": "debug_session", "lines": 1}, 1, "line-150")
	check(map[string]any{"agent_ref": "debug_session", "lines": 100}, 100, "line-51")
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, lines := range []int{0, 101} {
		structured := call(map[string]any{"agent_ref": "debug_session", "lines": lines})
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

func TestTSK630DebugTailAllowsPlannerAndLeadDurableSessions(t *testing.T) {
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

	fixture.server.Service.Config.ProjectAgentBindings = nil
	runtimes := []struct {
		agentRef string
		role     string
	}{
		{agentRef: "runtime-tsk630-planner", role: durableSession.RolePlanner},
		{agentRef: "runtime-tsk630-lead", role: durableSession.RoleLead},
		{agentRef: "runtime-tsk630-advisor", role: durableSession.RoleAdvisor},
		{agentRef: "runtime-tsk630-worker", role: durableSession.RoleWorker},
	}
	for _, item := range runtimes {
		fixture.sessions[item.role] = fixture.addSession(t, fixture.projectID, "EXM", item.role, item.agentRef)
	}
	for _, item := range runtimes {
		result := fixture.call(t, fixture.sessions[item.role], "debug/tail", map[string]any{"agent_ref": item.agentRef, "lines": 1})
		if result["ok"] != true {
			t.Fatalf("authenticated debug/tail role=%s durable_session=%s was rejected: %#v", item.role, fixture.sessions[item.role], result)
		}
	}
}
