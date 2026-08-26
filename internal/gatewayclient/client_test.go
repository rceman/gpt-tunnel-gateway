package gatewayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestTaskFinalizeUsesGatewayTransportAndReusesReceipt(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Method != "tools/call" {
			t.Fatalf("method = %q", request.Method)
		}
		index := calls.Add(1)
		structured := map[string]any{}
		switch index {
		case 1:
			if request.Params.Name != "session_start" || request.Params.Arguments["project"] != "GTW" {
				t.Fatalf("session_start request = %#v", request.Params)
			}
			structured["session"] = "SD-TEST1234"
		case 2, 3:
			if request.Params.Name != "call" || request.Params.Arguments["session"] != "SD-TEST1234" {
				t.Fatalf("call request = %#v", request.Params)
			}
			input, ok := request.Params.Arguments["input"].(map[string]any)
			if !ok || input["task_id"] != "GTW-TSK430" {
				t.Fatalf("task/finalize input = %#v", request.Params.Arguments["input"])
			}
			status := "accepted"
			if index == 3 {
				status = "completed"
			}
			structured = map[string]any{"result": map[string]any{"status": status, "operation_id": "op-1"}, "is_error": false}
		case 4:
			if request.Params.Name != "call" || request.Params.Arguments["action"] != "session/end" {
				t.Fatalf("session/end request = %#v", request.Params)
			}
			structured = map[string]any{"result": map[string]any{"status": "ended"}, "is_error": false}
		default:
			t.Fatalf("unexpected call %d", index)
		}
		writeToolResult(t, w, structured)
	}))
	defer server.Close()

	address := strings.TrimPrefix(server.URL, "http://")
	client := New(config.Config{
		ListenAddr: address,
		Projects: map[string]config.ProjectConfig{
			"gpt-tunnel-gateway": {ProjectCode: "GTW"},
		},
	})
	result, err := client.TaskFinalize(context.Background(), "GTW-TSK430")
	if err != nil {
		t.Fatalf("TaskFinalize: %v", err)
	}
	if result["status"] != "completed" || calls.Load() != 4 {
		t.Fatalf("result=%#v calls=%d", result, calls.Load())
	}
}

func TestTaskFinalizeFailsClosedWhenGatewayUnavailable(t *testing.T) {
	client := New(config.Config{
		ListenAddr: "127.0.0.1:1",
		Projects: map[string]config.ProjectConfig{
			"gpt-tunnel-gateway": {ProjectCode: "GTW"},
		},
	})
	_, err := client.TaskFinalize(context.Background(), "GTW-TSK430")
	if err == nil || !strings.Contains(err.Error(), "Gateway transport") {
		t.Fatalf("error = %v", err)
	}
}

func writeToolResult(t *testing.T, w http.ResponseWriter, structured map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"isError":           false,
			"structuredContent": structured,
		},
	}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
