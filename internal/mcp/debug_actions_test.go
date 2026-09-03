package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestDebugDomainIsAbsentWhenDisabled(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{StateDir: t.TempDir()}, nil)}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"debug/status", "debug/prompt", "debug/activate"} {
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
		if raw == "debug" {
			t.Fatal("disabled debug domain was discoverable")
		}
	}
	record, err := durableSession.NewStore(server.Service.Config.StateDir).CreateUnbound(durableSession.RolePlanner, nil)
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
	if structured["ok"] != false || !strings.Contains(structured["error"].(map[string]any)["message"].(string), "unknown action") {
		t.Fatalf("disabled debug action was callable: %#v", response)
	}
}

func TestEnabledDebugDomainHasExactInitialActions(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		Debug:    config.DebugConfig{Enabled: true},
		StateDir: t.TempDir(),
	}, nil)}
	entries := server.genericActionRegistry(server.tools())
	want := map[string]bool{"debug/status": true, "debug/prompt": true, "debug/activate": true}
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
	if len(actions) != len(want) {
		t.Fatalf("debug schema actions=%#v want=%v", actions, want)
	}
	for _, action := range actions {
		if !want[action["path"].(string)] {
			t.Fatalf("unexpected debug schema action=%#v", action)
		}
	}
}

func TestDebugStatusUsesOnlyConfiguredHostLocalState(t *testing.T) {
	_, sourceRoot, _ := testutil.RepoWithBareRemote(t)
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		Debug:      config.DebugConfig{Enabled: true},
		StateDir:   t.TempDir(),
		Projects:   map[string]config.ProjectConfig{gatewaySourceProjectID: {Root: sourceRoot}},
		GatewayID:  "debug-test",
		ListenAddr: "127.0.0.1:1",
	}, nil), AuthorityContext: authority.WithPlanner(context.Background())}
	record, err := durableSession.NewStore(server.Service.Config.StateDir).CreateUnbound(durableSession.RolePlanner, nil)
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
	if structured["ok"] != true {
		t.Fatalf("debug/status failed: %#v", response)
	}
	result := structured["result"].(map[string]any)
	if result["debug_enabled"] != true || result["source"].(map[string]any)["root"] != sourceRoot {
		t.Fatalf("debug/status omitted host-local source identity: %#v", result)
	}
	if result["gateway_id"] != "debug-test" {
		t.Fatalf("debug/status gateway_id=%#v want debug-test", result["gateway_id"])
	}
	if result["source"].(map[string]any)["clean"] != true {
		t.Fatalf("debug/status reported clean fixture as dirty: %#v", result["source"])
	}
}

func TestDebugActivatePublicMCPRequestUsesExactSourceAndReturnsHandoffIdentity(t *testing.T) {
	old := debugActivateFn
	defer func() { debugActivateFn = old }()
	_, sourceRoot, _ := testutil.RepoWithBareRemote(t)
	wantHead := strings.Repeat("a", 40)
	var gotRoot, gotHead string
	debugActivateFn = func(_ context.Context, c config.Config, _ string, project config.ProjectConfig, sourceHead string) (activation.Result, error) {
		gotRoot, gotHead = project.Root, sourceHead
		if c.GatewayID != "debug-test" {
			t.Fatalf("activation gateway_id=%q", c.GatewayID)
		}
		return activation.Result{SourceHead: sourceHead, Activation: "passed", Smoke: "passed", TunnelPID: 71, GatewayPID: 83}, nil
	}
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		Debug:     config.DebugConfig{Enabled: true},
		StateDir:  t.TempDir(),
		GatewayID: "debug-test",
		Projects:  map[string]config.ProjectConfig{gatewaySourceProjectID: {Root: sourceRoot}},
	}, nil), AuthorityContext: authority.WithPlanner(context.Background())}
	record, err := durableSession.NewStore(server.Service.Config.StateDir).CreateUnbound(durableSession.RolePlanner, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	if result["source_head"] != wantHead || result["activation"] != "passed" || result["tunnel_pid"] != float64(71) || result["gateway_pid"] != float64(83) {
		t.Fatalf("unexpected debug/activate result: %#v", result)
	}
	if gotRoot != sourceRoot || gotHead != wantHead {
		t.Fatalf("activation received root=%q head=%q, want root=%q head=%q", gotRoot, gotHead, sourceRoot, wantHead)
	}
}

func TestDebugPromptUsesDirectAirelayUnderBrokenNormalAuthority(t *testing.T) {
	script := filepath.Join(t.TempDir(), "airelay")
	contents := "#!/bin/sh\nif [ \"$1\" = prompt ] && [ \"$2\" = debug_session ] && [ \"$3\" = \"[GTW] recovery message\" ]; then exit 0; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{
		Debug:                  config.DebugConfig{Enabled: true},
		StateDir:               t.TempDir(),
		AirelayCommand:         script,
		DispatchTimeoutSeconds: 5,
	}, nil), AuthorityContext: authority.WithPlanner(context.Background())}
	record, err := durableSession.NewStore(server.Service.Config.StateDir).CreateUnbound(durableSession.RolePlanner, nil)
	if err != nil {
		t.Fatal(err)
	}
	response := callMCPRaw(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": record.ID, "action": "debug/prompt", "input": map[string]any{
				"airelay_session": "debug_session", "message": "recovery message",
			},
		}},
	}))
	structured := typedStructured(t, response)
	if structured["ok"] != true {
		t.Fatalf("direct debug/prompt failed: %#v", response)
	}
	result := structured["result"].(map[string]any)
	if result["status"] != "accepted" || result["airelay_session"] != "debug_session" {
		t.Fatalf("unexpected direct debug/prompt result: %#v", result)
	}
	entry := server.genericActionRegistry(server.tools())["debug/prompt"]
	if entry.Authority != nil || !entry.LocalReceiptOnly || entry.SessionBound {
		t.Fatalf("debug/prompt retained normal authority routing: %#v", entry)
	}
}
