package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestToolsListAndToolResultsUseObjects(t *testing.T) {
	c := config.Config{GatewayID: "test", ListenAddr: "127.0.0.1:1", MaxReadBytes: 1, MaxDiffBytes: 1, MaxListItems: 1}
	srv := &Server{Service: service.New(c)}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(body))
	req.Host = "127.0.0.1:1"
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response["result"].(map[string]any)
	if _, ok := result["tools"].([]any); !ok {
		t.Fatalf("tools missing: %#v", result)
	}
	call := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"system_ping","arguments":{}}}`)
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(call))
	req.Host = "127.0.0.1:1"
	req.RemoteAddr = "127.0.0.1:1234"
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result = response["result"].(map[string]any)
	if _, ok := result["structuredContent"].(map[string]any); !ok {
		t.Fatalf("structuredContent is not object: %#v", result)
	}
}

func TestMCPServerAuthorityBoundaryIsTrustedAndNonSerialized(t *testing.T) {
	state := t.TempDir()
	bare, _, _ := testutil.RepoWithBareRemote(t)
	serviceConfig := config.Config{StateDir: state, Hub: config.HubConfig{RepositoryURL: bare, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"}}
	svc := service.New(serviceConfig)
	if err := svc.Hub.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	beforeHub, err := svc.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{
		"operation_id": "11111111-1111-1111-1111-111111111111",
		"request":      map[string]any{"unexpected": true},
	}
	body := mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "project_onboard", "arguments": arguments, "_meta": map[string]any{"role": "delivery"}},
	})
	without := callMCP(t, &Server{Service: svc}, body)
	withoutResult := without["result"].(map[string]any)
	withoutText := withoutResult["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(withoutText, "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("unconfigured server did not fail closed: %q", withoutText)
	}
	afterHub, err := svc.Hub.RemoteRevision(context.Background())
	if err != nil || afterHub != beforeHub {
		t.Fatalf("unauthorized MCP call changed Hub: before=%s after=%s err=%v", beforeHub, afterHub, err)
	}
	afterState, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterState) != len(beforeState) {
		t.Fatalf("unauthorized MCP call changed local state: before=%v after=%v", beforeState, afterState)
	}
	with := callMCP(t, &Server{Service: svc, AuthorityContext: authority.WithDelivery(context.Background())}, body)
	withResult := with["result"].(map[string]any)
	withText := withResult["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(withText, "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("trusted server ignored configured authority: %q", withText)
	}
	if strings.Contains(withText, "delivery") {
		t.Fatalf("serialized _meta influenced authority: %q", withText)
	}
}

func TestGatewayCapabilitiesExposeManagedHub(t *testing.T) {
	state := t.TempDir()
	c := config.Config{GatewayID: "home_pc", ListenAddr: "127.0.0.1:8875", StateDir: state, Hub: config.HubConfig{RepositoryURL: "git@github.com:rceman/typer.git", Branch: "gpt-tunnel/home_pc"}}
	srv := &Server{Service: service.New(c)}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gateway_capabilities","arguments":{}}}`)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(body))
	req.Host = "127.0.0.1:1"
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response["result"].(map[string]any)["structuredContent"].(map[string]any)
	if result["hub_repository_url"] != c.Hub.RepositoryURL || result["hub_branch"] != c.Hub.Branch {
		t.Fatalf("unexpected hub capabilities: %#v", result)
	}
	wantRoot := filepath.Join(state, "hub", "repository")
	if result["hub_managed_root"] != wantRoot {
		t.Fatalf("managed root=%v want %s", result["hub_managed_root"], wantRoot)
	}
}

func TestRejectsNonLoopbackHost(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	req := httptest.NewRequest(http.MethodPost, "http://example.com/mcp", bytes.NewReader([]byte(`{}`)))
	req.Host = "example.com"
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
}
