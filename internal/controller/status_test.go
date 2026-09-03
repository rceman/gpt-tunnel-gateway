package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunningVersionUsesServerOwnedInitialize(t *testing.T) {
	var method string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		method = request.Method
		if method != "initialize" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"serverInfo": map[string]any{"version": "0.6.13", "gateway_id": "gateway"},
			},
		})
	}))
	defer server.Close()

	got := runningVersion(context.Background(), server.URL+"/readyz", "gateway")
	if got != "0.6.13" {
		t.Fatalf("runningVersion = %q, want 0.6.13", got)
	}
	if method != "initialize" {
		t.Fatalf("runningVersion used %q, want server-owned initialize", method)
	}
}
