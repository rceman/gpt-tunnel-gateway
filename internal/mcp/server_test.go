package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
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
