package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunningVersionUsesCanonicalStatusTool(t *testing.T) {
	var toolName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		toolName, _ = request.Params["name"].(string)
		if toolName == "system_ping" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"structuredContent": map[string]any{"version": "0.6.11", "gateway_id": "gateway"},
			},
		})
	}))
	defer server.Close()

	got := runningVersion(context.Background(), server.URL+"/readyz", "gateway")
	if got != "0.6.11" {
		t.Fatalf("runningVersion = %q, want 0.6.11", got)
	}
	if toolName != "status" {
		t.Fatalf("runningVersion called %q, want canonical status", toolName)
	}
}
