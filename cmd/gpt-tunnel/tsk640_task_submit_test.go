package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK640TaskSubmitUsesFixedAgentCLIEndpoints(t *testing.T) {
	const runtimeKey = "SA-EXAMPLE01"
	for command, endpoint := range map[string]string{
		"submit-code":   "/agent-cli/task/submit-code",
		"submit-tests":  "/agent-cli/task/submit-tests",
		"submit-rebase": "/agent-cli/task/submit-rebase",
	} {
		var method, path string
		var body map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method, path = r.Method, r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode %s request: %v", command, err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"key": "EXM-TSK1", "stage": "code", "status": "awaiting_review"}})
		}))
		t.Setenv("GPT_TUNNEL_SESSION", "")
		t.Setenv("AIRELAY_SESSION_KEY", runtimeKey)
		s := &service.Service{Config: config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}}
		result, err := taskSubmitGatewayCall(context.Background(), s, command)
		server.Close()
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if method != http.MethodPost || path != endpoint {
			t.Fatalf("%s transport=%s %s", command, method, path)
		}
		if len(body) != 1 || body["runtime"] != runtimeKey {
			t.Fatalf("%s body=%#v", command, body)
		}
		if result.(map[string]any)["key"] != "EXM-TSK1" {
			t.Fatalf("%s result=%#v", command, result)
		}
	}
}

func TestTSK640TaskSubmitRequiresAirelayRuntimeBeforeTransport(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	t.Setenv("AIRELAY_SESSION_KEY", "")
	s := &service.Service{Config: config.Config{ListenAddr: "127.0.0.1:1"}}
	if _, err := taskSubmitGatewayCall(context.Background(), s, "submit-code"); err == nil || !strings.Contains(err.Error(), "managed Airelay runtime identity is required") {
		t.Fatalf("missing runtime error=%v", err)
	}
}

func TestTSK640TaskSubmitRejectsMalformedRuntimeIdentityBeforeTransport(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer server.Close()
	t.Setenv("GPT_TUNNEL_SESSION", "")
	s := &service.Service{Config: config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}}
	for name, runtimeKey := range map[string]string{
		"empty":             "",
		"leading space":     " SA-EXAMPLE01",
		"trailing space":    "SA-EXAMPLE01 ",
		"surrounding space": " SA-EXAMPLE01 ",
		"trailing newline":  "SA-EXAMPLE01\n",
		"leading tab":       "\tSA-EXAMPLE01",
	} {
		t.Setenv("AIRELAY_SESSION_KEY", runtimeKey)
		if _, err := taskSubmitGatewayCall(context.Background(), s, "submit-code"); err == nil || !strings.Contains(err.Error(), "managed Airelay runtime identity is required") {
			t.Fatalf("%s runtime identity error=%v", name, err)
		}
	}
	if requests != 0 {
		t.Fatalf("malformed runtime identity reached the Gateway: requests=%d", requests)
	}
}

func TestTSK640TaskSubmitSurfacesCanonicalActionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": "ACTION_FAILED", "message": "Task is not accepting a tests submission"}})
	}))
	defer server.Close()
	t.Setenv("GPT_TUNNEL_SESSION", "")
	t.Setenv("AIRELAY_SESSION_KEY", "SA-EXAMPLE01")
	s := &service.Service{Config: config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}}
	_, err := taskSubmitGatewayCall(context.Background(), s, "submit-tests")
	if err == nil || !strings.Contains(err.Error(), "Task is not accepting a tests submission") {
		t.Fatalf("canonical failure error=%v", err)
	}
}

func TestTSK640TaskCurrentKeepsGenericMCPTransport(t *testing.T) {
	const runtimeKey = "SA-EXAMPLE01"
	var path string
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode current request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"structuredContent": map[string]any{"ok": true, "result": map[string]any{"key": "EXM-TSK1"}}}})
	}))
	defer server.Close()
	t.Setenv("GPT_TUNNEL_SESSION", "")
	t.Setenv("AIRELAY_SESSION_KEY", runtimeKey)
	s := &service.Service{Config: config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}}
	result, err := taskExecutionGatewayCall(context.Background(), s)
	if err != nil {
		t.Fatalf("task current: %v", err)
	}
	if path != "/mcp" || result.(map[string]any)["key"] != "EXM-TSK1" {
		t.Fatalf("task current transport=%s result=%#v", path, result)
	}
	arguments := request["params"].(map[string]any)["arguments"].(map[string]any)
	if arguments["session"] != runtimeKey || arguments["action"] != "task/current" {
		t.Fatalf("task current arguments=%#v", arguments)
	}
}
