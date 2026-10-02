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

func TestSessionAttachCLIRequestHitsOperatorRouteWithExactIdentity(t *testing.T) {
	stateDir := t.TempDir()
	if _, err := service.EnsureOperatorToken(stateDir); err != nil {
		t.Fatal(err)
	}
	var gotPath, gotMethod string
	var gotInput service.SessionAttachInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		if err := json.NewDecoder(r.Body).Decode(&gotInput); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"action":"attach","status":"attached","project_id":"example","project_code":"EXM","role":"worker","agent_id":"EXM-WORKER","session_ref":"exm_worker","session":{"session_id":"HOM_EXM_W_test","schema_version":1,"role":"worker","session_type":"chatgpt","status":"active"}}}`))
	}))
	defer server.Close()
	c := config.Config{StateDir: stateDir, ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	s := service.New(c)
	session(context.Background(), s, []string{"attach", "EXM", "worker", "EXM-WORKER"})
	if gotMethod != http.MethodPost || gotPath != "/operator/session/attach" {
		t.Fatalf("request %s %s", gotMethod, gotPath)
	}
	if gotInput.ProjectCode != "EXM" || gotInput.Role != "worker" || gotInput.AgentID != "EXM-WORKER" {
		t.Fatalf("attach input=%#v", gotInput)
	}
}
