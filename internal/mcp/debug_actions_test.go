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
	for _, path := range []string{"debug/status", "debug/prompt", "debug/tail", "debug/await", "debug/activate"} {
		if _, ok := entries[path]; ok {
			t.Fatalf("disabled debug action %q was registered", path)
		}
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
	want := map[string]bool{"debug/status": true, "debug/prompt": true, "debug/tail": true, "debug/await": true, "debug/activate": true}
	got := map[string]bool{}
	for path := range entries {
		if strings.HasPrefix(path, "debug/") {
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
		if entry.AuthorityRole != actionRolePlannerOrLead || !entry.SessionBound || !entry.SessionRequired {
			t.Fatalf("debug action %q authority/session contract=%#v", path, entry)
		}
		if path == "debug/activate" && !entry.Annotations.IdempotentHint {
			t.Fatal("debug/activate must advertise idempotent source-keyed ensure semantics")
		}
	}
	legacy := server.tools()
	root, err := server.genericSchema(legacy, []byte(`{"path":""}`))
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
	domain, err := server.genericSchema(legacy, []byte(`{"path":"debug"}`))
	if err != nil {
		t.Fatal(err)
	}
	actions := domain.(map[string]any)["actions"].([]map[string]any)
	if len(actions) != len(want) {
		t.Fatalf("debug schema actions=%#v want=%v", actions, want)
	}
	for _, action := range actions {
		if !want[action["path"].(string)] {
			t.Fatalf("unexpected debug schema action=%#v", action)
		}
	}
}

func TestEnabledDebugSchemaDiscoveryIsExactForPlannerAndLead(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{Debug: config.DebugConfig{Enabled: true}, StateDir: t.TempDir()})
	server := &Server{Service: s}
	store := mcpSQLiteSessionStore(t, s)
	want := map[string]bool{
		"debug/status":   true,
		"debug/prompt":   true,
		"debug/tail":     true,
		"debug/await":    true,
		"debug/activate": true,
	}
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead} {
		record := debugTestSession(t, store, role)
		if role == durableSession.RolePlanner {
			server.AuthorityContext = authority.WithPlanner(context.Background())
		} else {
			server.AuthorityContext = authority.WithLead(context.Background())
		}
		value, err := server.genericSchemaPublic(server.AuthorityContext, server.tools(), mustJSON(t, map[string]any{
			"session": record.ID, "path": "debug",
		}))
		if err != nil {
			t.Fatalf("%s schema discovery failed: %v", role, err)
		}
		domain := value.(map[string]any)
		actions := domain["actions"].([]map[string]any)
		got := make(map[string]bool, len(actions))
		for _, action := range actions {
			got[action["path"].(string)] = true
		}
		if len(got) != len(want) {
			t.Fatalf("%s debug schema paths=%v want=%v", role, got, want)
		}
		for path := range want {
			if !got[path] {
				t.Fatalf("%s debug schema omitted %q: %v", role, path, got)
			}
		}
	}
}

func TestDebugAgentRefIsIsolatedToDebugDomain(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{
		Debug:    config.DebugConfig{Enabled: true},
		StateDir: t.TempDir(),
	})
	server := &Server{Service: s}
	entries := server.genericActionRegistry(server.tools())
	var containsKey func(any, string) bool
	containsKey = func(value any, want string) bool {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				if key == want || containsKey(child, want) {
					return true
				}
			}
		case []any:
			for _, child := range current {
				if containsKey(child, want) {
					return true
				}
			}
		}
		return false
	}
	for path, entry := range entries {
		if strings.HasPrefix(path, "debug/") {
			continue
		}
		if containsKey(entry.InputSchema, "agent_ref") || containsKey(entry.OutputSchema, "agent_ref") {
			t.Fatalf("ordinary action %s exposes debug-only agent_ref", path)
		}
	}
	for _, path := range []string{"debug/prompt", "debug/tail", "debug/await"} {
		entry := entries[path]
		properties := entry.InputSchema["properties"].(map[string]any)
		if _, ok := properties["agent"]; ok {
			t.Fatalf("%s retained logical Agent selector", path)
		}
		if _, ok := properties["agent_ref"]; !ok || !containsRequired(stringList(entry.InputSchema["required"]), "agent_ref") {
			t.Fatalf("%s does not require direct agent_ref", path)
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
	if result["gateway"] != "HOM" {
		t.Fatalf("debug/status gateway=%#v want HOM", result["gateway"])
	}
	if result["source"].(map[string]any)["clean"] != true {
		t.Fatalf("debug/status reported clean fixture as dirty: %#v", result["source"])
	}
}

func TestDebugActivatePublicMCPRequestUsesExactSourceAndReturnsHandoffIdentity(t *testing.T) {
	old := debugActivationAcceptFn
	defer func() { debugActivationAcceptFn = old }()
	_, sourceRoot, _ := testutil.RepoWithBareRemote(t)
	var wantHead string
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
		Debug:        config.DebugConfig{Enabled: true},
		StateDir:     t.TempDir(),
		GatewayID:    "HOM",
		MaxDiffBytes: 1 << 20,
		MaxReadBytes: 1 << 20,
		Projects:     map[string]config.ProjectConfig{gatewaySourceProjectID: {Root: sourceRoot}},
	})
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	store := mcpSQLiteSessionStore(t, server.Service)
	record := debugTestSession(t, store, durableSession.RolePlanner)
	status, err := server.Service.Git.WorktreeStatus(context.Background(), config.ProjectConfig{Root: sourceRoot})
	if err != nil {
		t.Fatal(err)
	}
	wantHead = status.Head
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": record.ID, "action": "debug/activate", "input": map[string]any{"main_sha": wantHead[:8]},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] != true {
		t.Fatalf("debug/activate failed: %#v", response)
	}
	result := structured["result"].(map[string]any)
	if result["source_head"] != wantHead[:8] || result["activation"] != "accepted" || result["smoke"] != "pending" || result["outcome"] != "accepted" {
		t.Fatalf("unexpected debug/activate result: %#v", result)
	}
	if gotHead != wantHead {
		t.Fatalf("activation worker received head=%q, want %q", gotHead, wantHead)
	}
	if _, exists := result["operation_id"]; exists {
		t.Fatalf("debug/activate leaked an internal operation identity: %#v", result)
	}
}

func TestDebugPromptUsesDirectAirelayUnderBrokenNormalAuthority(t *testing.T) {
	script := filepath.Join(t.TempDir(), "airelay")
	contents := "#!/bin/sh\nif [ \"$1\" = prompt ] && [ \"$2\" = runtime-tsk571 ] && [ \"$3\" = \"[GTW] recovery message\" ]; then exit 0; fi\nif [ \"$1\" = tail ] && [ \"$2\" = runtime-tsk571 ] && [ \"$3\" = --lines ] && [ \"$4\" = 2 ]; then printf 'break-glass tail\\n'; exit 0; fi\nif [ \"$1\" = session-status ] && [ \"$2\" = runtime-tsk571 ]; then printf 'Controller: reachable\\nState: idle\\n'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleWorker}, true, true)
	fixture.server.Service.Config.Debug.Enabled = true
	fixture.server.Service.Airelay.Command = script
	fixture.server.Service.Config.AirelayCommand = script
	fixture.server.Service.Airelay.Timeout = 5 * time.Second
	fixture.server.Service.Durability.Shared = nil
	fixture.server.Service.Config.ProjectAgentBindings = nil
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
				"agent_ref": "runtime-tsk571", "message": "recovery message",
			},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] != true {
		t.Fatalf("direct debug/prompt failed: %#v", response)
	}
	result := structured["result"].(map[string]any)
	if result["status"] != "accepted" || result["agent_ref"] != "runtime-tsk571" {
		t.Fatalf("unexpected direct debug/prompt result: %#v", result)
	}
	tailResponse := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": recordID, "action": "debug/tail", "input": map[string]any{"agent_ref": "runtime-tsk571", "lines": 2},
		}},
	}))
	tailStructured := typedStructured(t, tailResponse)
	if tailStructured["ok"] != true {
		t.Fatalf("direct debug/tail failed while normal routing was unavailable: %#v", tailResponse)
	}
	tailResult := tailStructured["result"].(map[string]any)
	if tailResult["agent_ref"] != "runtime-tsk571" || tailResult["status"] != "ok" {
		t.Fatalf("unexpected direct debug/tail result: %#v", tailResult)
	}
	awaitResponse := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": recordID, "action": "debug/await", "input": map[string]any{"agent_ref": "runtime-tsk571", "seconds": 1, "lines": 2},
		}},
	}))
	awaitStructured := typedStructured(t, awaitResponse)
	if awaitStructured["ok"] != true {
		t.Fatalf("direct debug/await failed while normal routing was unavailable: %#v", awaitResponse)
	}
	awaitResult := awaitStructured["result"].(map[string]any)
	if awaitResult["agent_ref"] != "runtime-tsk571" || awaitResult["status"] != "idle" {
		t.Fatalf("unexpected direct debug/await result: %#v", awaitResult)
	}
	entry := server.genericActionRegistry(server.tools())["debug/prompt"]
	if entry.Authority != nil || !entry.LocalReceiptOnly || !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("debug/prompt contract changed: %#v", entry)
	}
	if entry.AuthorityRole != actionRolePlannerOrLead {
		t.Fatalf("debug/prompt authority role=%q want %q", entry.AuthorityRole, durableSession.RolePlanner)
	}
}

func TestDebugAwaitRequiresExplicitSeconds(t *testing.T) {
	s, _ := mcpServiceWithSQLite(t, config.Config{Debug: config.DebugConfig{Enabled: true}, StateDir: t.TempDir()})
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	entry := server.genericActionRegistry(server.tools())["debug/await"]
	required := stringList(entry.InputSchema["required"])
	if len(required) != 2 || !containsRequired(required, "agent_ref") || !containsRequired(required, "seconds") {
		t.Fatalf("debug/await required fields=%v", required)
	}
	seconds := entry.InputSchema["properties"].(map[string]any)["seconds"].(map[string]any)
	if _, ok := seconds["default"]; ok {
		t.Fatal("debug/await seconds unexpectedly has a default")
	}
	record := debugTestSession(t, mcpSQLiteSessionStore(t, server.Service), durableSession.RolePlanner)
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": record.ID, "action": "debug/await", "input": map[string]any{"agent_ref": "runtime-tsk628"},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] == true {
		t.Fatalf("debug/await accepted omitted seconds: %#v", response)
	}
	message := structured["error"].(map[string]any)["message"].(string)
	if !strings.Contains(message, "seconds") {
		t.Fatalf("omitted seconds rejection=%q", message)
	}
}

func TestDebugActionsAuthorizeEachRoleBeforeExecution(t *testing.T) {
	oldActivation := debugActivationAcceptFn
	defer func() { debugActivationAcceptFn = oldActivation }()
	_, sourceRoot, _ := testutil.RepoWithBareRemote(t)
	logPath := filepath.Join(t.TempDir(), "airelay-calls")
	script := filepath.Join(t.TempDir(), "airelay")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"" + logPath + "\"\ncase \"$1\" in\nprompt) exit 0;;\ntail) printf 'bounded debug tail\\n'; exit 0;;\nsession-status) printf 'Controller: reachable\\nState: idle\\n'; exit 0;;\n*) exit 0;;\nesac\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}, true, true)
	sourceHead := strings.TrimSpace(testutil.Git(t, sourceRoot, "rev-parse", "HEAD"))
	fixture.server.Service.Config.Debug.Enabled = true
	if fixture.server.Service.Config.Projects == nil {
		fixture.server.Service.Config.Projects = map[string]config.ProjectConfig{}
	}
	fixture.server.Service.Config.Projects[gatewaySourceProjectID] = config.ProjectConfig{Root: sourceRoot}
	fixture.server.Service.Config.ProjectAgentBindings = nil
	fixture.server.Service.Airelay.Command = script
	fixture.server.Service.Config.AirelayCommand = script
	fixture.server.Service.Airelay.Timeout = 5 * time.Second
	var activationCalls int
	debugActivationAcceptFn = func(c config.Config, _ string, sourceHead string, _ func(func())) (debugdomain.ActivationResult, error) {
		activationCalls++
		return debugdomain.ActivationResult{OperationID: "debug-auth-test", SourceHead: sourceHead, Activation: "accepted", Smoke: "pending", Outcome: "accepted"}, nil
	}
	actions := []struct {
		path  string
		input map[string]any
	}{
		{path: "debug/status", input: map[string]any{}},
		{path: "debug/prompt", input: map[string]any{"agent_ref": "runtime-tsk628", "message": "bounded auth test"}},
		{path: "debug/tail", input: map[string]any{"agent_ref": "runtime-tsk628", "lines": 1}},
		{path: "debug/await", input: map[string]any{"agent_ref": "runtime-tsk628", "seconds": 1, "lines": 1}},
		{path: "debug/activate", input: map[string]any{"main_sha": sourceHead[:8]}},
	}
	roles := []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}
	for _, action := range actions {
		for _, role := range roles {
			beforeCalls, err := os.ReadFile(logPath)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			beforeActivation := activationCalls
			result := fixture.call(t, fixture.sessions[role], action.path, action.input)
			if result["ok"] != true {
				t.Fatalf("%s role=%s was rejected after authentication: %#v", action.path, role, result)
			}
			if action.path == "debug/prompt" || action.path == "debug/tail" || action.path == "debug/await" {
				afterCalls, readErr := os.ReadFile(logPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(afterCalls) <= len(beforeCalls) {
					t.Fatalf("%s role=%s did not reach its bounded stub", action.path, role)
				}
			}
			if action.path == "debug/activate" && activationCalls != beforeActivation+1 {
				t.Fatalf("%s role=%s did not reach activation stub", action.path, role)
			}
		}
	}
}
