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

func TestTSK567TaskReadUsesGatewaySessionAuthority(t *testing.T) {
	const session = "SA-EXAMPLE01"
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode Gateway request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"structuredContent": map[string]any{"ok": true, "result": map[string]any{"key": "EXM-TSK1", "revision": 1, "title": "Task", "summary": "Summary", "status": "planned", "objective": "Objective"}}}})
	}))
	defer server.Close()

	t.Setenv("GPT_TUNNEL_SESSION", session)
	s := &service.Service{Config: config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}}
	result, err := taskReadGatewayCall(context.Background(), s, "EXM-TSK1")
	if err != nil {
		t.Fatalf("task read: %v", err)
	}
	if result.(map[string]any)["key"] != "EXM-TSK1" {
		t.Fatalf("task read result=%#v", result)
	}
	params := request["params"].(map[string]any)
	arguments := params["arguments"].(map[string]any)
	if arguments["session"] != session || arguments["action"] != "task/read" {
		t.Fatalf("Gateway authority request=%#v", arguments)
	}
	input := arguments["input"].(map[string]any)
	if len(input) != 1 || input["key"] != "EXM-TSK1" {
		t.Fatalf("Task read input=%#v", input)
	}
}

func TestTSK567TaskReadRequiresGatewaySessionBeforeTransport(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	s := &service.Service{Config: config.Config{ListenAddr: "127.0.0.1:1"}}
	if _, err := taskReadGatewayCall(context.Background(), s, "EXM-TSK1"); err == nil || !strings.Contains(err.Error(), "Gateway session authority is required") {
		t.Fatalf("missing session error=%v", err)
	}
}
