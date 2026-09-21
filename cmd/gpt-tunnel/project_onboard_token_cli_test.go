package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcp"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestProjectOnboardCLIExposesDurablePlannerToken(t *testing.T) {
	workdir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "gpt-tunnel")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = workdir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	hubBare, projectRoot, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, projectRoot, "remote", "set-head", "origin", "main")
	stateDir := filepath.Join(t.TempDir(), "state")
	airelay := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) if [ \"$3\" = --json ]; then printf '{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"; else printf 'Controller: reachable\\nState: idle\\n'; fi ;;\nstatus) printf 'Controller: reachable\\nState: idle\\n' ;;\n*) exit 99 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{
		SchemaVersion: 1, GatewayID: "HOM", ListenAddr: "127.0.0.1:8875", StateDir: stateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20,
		MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: airelay,
		Hub:        config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
		Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8876"},
		Projects:   map[string]config.ProjectConfig{},
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (hub.Store{Config: cfg}).Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = workdir
		cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gpt-tunnel %v failed: %v\n%s", args, err, output)
		}
		return string(output)
	}
	var first, retry map[string]any
	if err := json.Unmarshal([]byte(run("project", "onboard", "--root", projectRoot, "AIR", "agentir_worker")), &first); err != nil {
		t.Fatal(err)
	}
	if first["status"] != "onboarded" || first["token"] == "" || first["token_usage"] != service.ProjectOnboardTokenUsage {
		t.Fatal("fresh onboarding did not return the expected token contract")
	}
	token := first["token"].(string)
	if agents, ok := first["agents"].([]any); !ok || len(agents) != 1 || agents[0].(map[string]any)["agent"] != "AIR-WORKER" {
		t.Fatalf("onboard agents=%#v", first["agents"])
	}
	if err := json.Unmarshal([]byte(run("project", "onboard", "--root", projectRoot, "AIR", "agentir_worker")), &retry); err != nil {
		t.Fatal(err)
	}
	if retry["status"] != "already_registered" || retry["token"] != token || retry["token_usage"] != service.ProjectOnboardTokenUsage {
		t.Fatal("repeat onboarding did not return the stable token contract")
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(loaded.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := service.NewWithDurabilityDeferredWorkers(loaded, db)
	svc.ConfigPath = configPath
	server := &mcp.Server{Service: svc, AuthorityContext: authority.WithPlanner(context.Background())}
	started := tsk652MCPTool(t, server, "session_start", map[string]any{"token": token})
	sessionID, _ := started["session"].(string)
	if sessionID == "" || started["role"] != "planner" {
		t.Fatalf("session_start output=%#v", started)
	}
	mcpSurfaces := []any{started, tsk652MCPTool(t, server, "call", map[string]any{"session": sessionID, "action": "project/status", "input": map[string]any{}}), tsk652MCPTool(t, server, "call", map[string]any{"session": sessionID, "action": "session/list", "input": map[string]any{}}), tsk652MCPTool(t, server, "call", map[string]any{"session": sessionID, "action": "session/info", "input": map[string]any{}}), tsk652MCPTool(t, server, "call", map[string]any{"session": sessionID, "action": "agent/guide", "input": map[string]any{}}), tsk652MCPTool(t, server, "call", map[string]any{"session": sessionID, "action": "runtime/logs", "input": map[string]any{}}), tsk652MCPTool(t, server, "tools/list", map[string]any{})}
	encoded, err := json.Marshal(mcpSurfaces)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("project bootstrap token leaked through MCP output")
	}
}

func tsk652MCPTool(t *testing.T, server *mcp.Server, name string, arguments map[string]any) map[string]any {
	t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call"}
	if name == "tools/list" {
		request["method"] = "tools/list"
	} else {
		request["params"] = map[string]any{"name": name, "arguments": arguments}
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(body))
	req.Host = "127.0.0.1:1"
	req.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, req)
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode MCP response: %v\n%s", err, recorder.Body.String())
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("MCP %s failed: %#v", name, response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		return result
	}
	if name == "call" {
		if structured["is_error"] == true {
			t.Fatalf("MCP call failed: %#v", structured)
		}
		actionResult, ok := structured["result"].(map[string]any)
		if !ok {
			t.Fatalf("MCP call omitted action result: %#v", structured)
		}
		return actionResult
	}
	return structured
}
