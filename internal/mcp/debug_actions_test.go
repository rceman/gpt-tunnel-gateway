package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	debugdomain "github.com/rceman/gpt-tunnel-gateway/internal/debug"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestDebugDomainIsAbsentWhenDisabled(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{StateDir: t.TempDir()})
	server := &Server{Service: s}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"debug/status", "debug/prompt", "debug/tail", "debug/activate"} {
		if _, ok := entries[path]; ok {
			t.Fatalf("disabled debug action %q was registered", path)
		}
	}
	root, err := server.genericSchema(nil, []byte(`{"path":""}`))
	if err != nil {
		t.Fatal(err)
	}
	rootObject := root.(map[string]any)
	for _, raw := range rootObject["domains"].([]string) {
		// The TSK409 read-only ADR relation action is always available under
		// debug; runtime debug actions remain disabled and are checked below.
		_ = raw
	}
	record := debugTestSession(t, mcpSQLiteSessionStore(t, server.Service), durableSession.RolePlanner)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": record.ID, "action": "debug/status", "input": map[string]any{},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] != false || !strings.Contains(structured["error"].(map[string]any)["message"].(string), "unknown action") {
		t.Fatalf("disabled debug action was callable: %#v", response)
	}
}

func TestEnabledDebugDomainHasExactInitialActions(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{
		Debug:    config.DebugConfig{Enabled: true},
		StateDir: t.TempDir(),
	})
	server := &Server{Service: s}
	entries := server.genericActionRegistry(server.tools())
	want := map[string]bool{"debug/status": true, "debug/prompt": true, "debug/tail": true, "debug/activate": true}
	got := map[string]bool{}
	for path := range entries {
		if strings.HasPrefix(path, "debug/") {
			if isAlwaysAvailableDebugEvidence(path) {
				continue
			}
			got[path] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("enabled debug actions=%v want=%v", got, want)
	}
	for path := range want {
		if !got[path] {
			t.Fatalf("enabled debug actions omitted %q: %v", path, got)
		}
		entry := entries[path]
		if entry.AuthorityRole != durableSession.RolePlanner || !entry.SessionBound || !entry.SessionRequired {
			t.Fatalf("debug action %q authority/session contract=%#v", path, entry)
		}
		if path == "debug/activate" && !entry.Annotations.IdempotentHint {
			t.Fatal("debug/activate must advertise idempotent source-keyed ensure semantics")
		}
	}
	root, err := server.genericSchema(nil, []byte(`{"path":""}`))
	if err != nil {
		t.Fatal(err)
	}
	foundDomain := false
	rootObject := root.(map[string]any)
	for _, raw := range rootObject["domains"].([]string) {
		if raw == "debug" {
			foundDomain = true
		}
	}
	if !foundDomain {
		t.Fatal("enabled debug domain was not discoverable")
	}
	domain, err := server.genericSchema(nil, []byte(`{"path":"debug"}`))
	if err != nil {
		t.Fatal(err)
	}
	actions := domain.(map[string]any)["actions"].([]map[string]any)
	filtered := make([]map[string]any, 0, len(actions))
	for _, action := range actions {
		if isAlwaysAvailableDebugEvidence(action["path"].(string)) {
			continue
		}
		filtered = append(filtered, action)
	}
	actions = filtered
	if len(actions) != len(want) {
		t.Fatalf("debug schema actions=%#v want=%v", actions, want)
	}
	for _, action := range actions {
		if !want[action["path"].(string)] {
			t.Fatalf("unexpected debug schema action=%#v", action)
		}
	}
}

func debugTestSession(t *testing.T, store durableSession.Store, role string) durableSession.Record {
	t.Helper()
	input := durableSession.CreateInput{ProjectID: gatewaySourceProjectID, ProjectCode: "GTW", Role: role, SessionType: durableSession.SessionTypeChatGPT}
	if durableSession.WorkflowRoleRequiresRef(role) {
		ref := "runtime-worker"
		input.SessionRef = &ref
	}
	record, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestDebugStatusUsesOnlyConfiguredHostLocalState(t *testing.T) {
	_, sourceRoot, _ := testutil.RepoWithBareRemote(t)
	s, _ := mcpServiceWithSQLite(t, config.Config{
		Debug:      config.DebugConfig{Enabled: true},
		StateDir:   t.TempDir(),
		Projects:   map[string]config.ProjectConfig{gatewaySourceProjectID: {Root: sourceRoot}},
		GatewayID:  "HOM",
		ListenAddr: "127.0.0.1:1",
	})
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	store := mcpSQLiteSessionStore(t, server.Service)
	record := debugTestSession(t, store, durableSession.RolePlanner)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": record.ID, "action": "debug/status", "input": map[string]any{},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] != true {
		t.Fatalf("debug/status failed: %#v", response)
	}
	result := structured["result"].(map[string]any)
	if result["debug_enabled"] != true || result["source"].(map[string]any)["root"] != sourceRoot {
		t.Fatalf("debug/status omitted host-local source identity: %#v", result)
	}
	if result["gateway_id"] != "HOM" {
		t.Fatalf("debug/status gateway_id=%#v want debug-test", result["gateway_id"])
	}
	if result["source"].(map[string]any)["clean"] != true {
		t.Fatalf("debug/status reported clean fixture as dirty: %#v", result["source"])
	}
}

func TestDebugActivatePublicMCPRequestUsesExactSourceAndReturnsHandoffIdentity(t *testing.T) {
	old := debugActivationAcceptFn
	defer func() { debugActivationAcceptFn = old }()
	_, sourceRoot, _ := testutil.RepoWithBareRemote(t)
	wantHead := strings.Repeat("a", 40)
	var gotHead string
	debugActivationAcceptFn = func(c config.Config, _ string, sourceHead string, _ func(func())) (debugdomain.ActivationResult, error) {
		gotHead = sourceHead
		if c.GatewayID != "HOM" {
			t.Fatalf("activation gateway_id=%q", c.GatewayID)
		}
		return debugdomain.ActivationResult{
			OperationID: "debug-test-operation", SourceHead: sourceHead,
			Activation: "accepted", Smoke: "pending", Outcome: "accepted",
		}, nil
	}
	s, _ := mcpServiceWithSQLite(t, config.Config{
		Debug:     config.DebugConfig{Enabled: true},
		StateDir:  t.TempDir(),
		GatewayID: "HOM",
		Projects:  map[string]config.ProjectConfig{gatewaySourceProjectID: {Root: sourceRoot}},
	})
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	store := mcpSQLiteSessionStore(t, server.Service)
	record := debugTestSession(t, store, durableSession.RolePlanner)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": record.ID, "action": "debug/activate", "input": map[string]any{"main_sha": wantHead},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] != true {
		t.Fatalf("debug/activate failed: %#v", response)
	}
	result := structured["result"].(map[string]any)
	if result["source_head"] != wantHead || result["activation"] != "accepted" || result["smoke"] != "pending" || result["outcome"] != "accepted" {
		t.Fatalf("unexpected debug/activate result: %#v", result)
	}
	if gotHead != wantHead {
		t.Fatalf("activation worker received head=%q, want %q", gotHead, wantHead)
	}
}

func TestDebugPromptUsesDirectAirelayUnderBrokenNormalAuthority(t *testing.T) {
	script := filepath.Join(t.TempDir(), "airelay")
	contents := "#!/bin/sh\nif [ \"$1\" = prompt ] && [ \"$2\" = runtime-tsk571 ] && [ \"$3\" = \"[GTW] recovery message\" ]; then exit 0; fi\nif [ \"$1\" = tail ] && [ \"$2\" = runtime-tsk571 ] && [ \"$3\" = --lines ] && [ \"$4\" = 2 ]; then printf 'break-glass tail\\n'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleWorker}, true, true)
	fixture.server.Service.Config.Debug.Enabled = true
	fixture.server.Service.Airelay.Command = script
	fixture.server.Service.Config.AirelayCommand = script
	fixture.server.Service.Airelay.Timeout = 5 * time.Second
	fixture.server.Service.Durability.Shared = nil
	hubLock, err := lockfile.Acquire(filepath.Join(fixture.server.Service.Config.StateDir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal(err)
	}
	defer hubLock.Release()
	server := fixture.server
	recordID := fixture.sessions[durableSession.RolePlanner]
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": recordID, "action": "debug/prompt", "input": map[string]any{
				"agent": fixture.agentID, "message": "recovery message",
			},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] != true {
		t.Fatalf("direct debug/prompt failed: %#v", response)
	}
	result := structured["result"].(map[string]any)
	if result["status"] != "accepted" || result["agent"] != fixture.agentID {
		t.Fatalf("unexpected direct debug/prompt result: %#v", result)
	}
	tailResponse := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": recordID, "action": "debug/tail", "input": map[string]any{"agent": fixture.agentID, "lines": 2},
		}},
	}))
	tailStructured := typedStructured(t, tailResponse)
	if tailStructured["ok"] != true {
		t.Fatalf("direct debug/tail failed while normal routing was unavailable: %#v", tailResponse)
	}
	tailResult := tailStructured["result"].(map[string]any)
	if tailResult["agent"] != fixture.agentID || tailResult["status"] != "ok" {
		t.Fatalf("unexpected direct debug/tail result: %#v", tailResult)
	}
	entry := server.genericActionRegistry(server.tools())["debug/prompt"]
	if entry.Authority != nil || !entry.LocalReceiptOnly || !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("debug/prompt contract changed: %#v", entry)
	}
	if entry.AuthorityRole != durableSession.RolePlanner {
		t.Fatalf("debug/prompt authority role=%q want %q", entry.AuthorityRole, durableSession.RolePlanner)
	}
}

func TestDebugActionsRejectNonPlannerSessions(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{
		Debug:    config.DebugConfig{Enabled: true},
		StateDir: t.TempDir(),
	})
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	store := mcpSQLiteSessionStore(t, server.Service)
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		input := durableSession.CreateInput{ProjectID: gatewaySourceProjectID, ProjectCode: "GTW", Role: role, SessionType: durableSession.SessionTypeChatGPT}
		if durableSession.WorkflowRoleRequiresRef(role) {
			ref := "runtime-worker"
			input.SessionRef = &ref
		}
		record, err := store.Create(input)
		if err != nil {
			t.Fatal(err)
		}
		response := callMCPRaw(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session": record.ID, "action": "debug/status", "input": map[string]any{},
			}},
		}))
		structured := typedStructured(t, response)
		if structured["ok"] != false {
			t.Fatalf("debug/status accepted %s session: %#v", role, response)
		}
	}
}
