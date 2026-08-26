package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestToolsListAndToolResultsUseObjects(t *testing.T) {
	c := config.Config{GatewayID: "test", ListenAddr: "127.0.0.1:1", MaxReadBytes: 1, MaxDiffBytes: 1, MaxListItems: 1}
	srv := &Server{
		Service:          service.New(c),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
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
	call := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"schema","arguments":{"path":""}}}`)
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
	server := &Server{Service: svc}
	if err := server.RegisterGenericAction(GenericAction{
		Path: "test/authority", Description: "authority boundary test", InputSchema: obj(map[string]any{}),
		OutputSchema:  closedOutput(map[string]any{"ok": outputBoolean()}, "ok"),
		AuthorityRole: durableSession.RoleDelivery, Authority: authority.RequireDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := authority.RequireDelivery(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	record, err := durableSession.NewStore(state).Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleDelivery, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
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
	arguments := map[string]any{"session": record.ID, "action": "test/authority", "input": map[string]any{}}
	body := mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": arguments, "_meta": map[string]any{"role": "delivery"}},
	})
	without := callMCP(t, server, body)
	withoutText, _ := json.Marshal(without)
	if !strings.Contains(string(withoutText), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("unconfigured server did not fail closed: %s", withoutText)
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
	server.AuthorityContext = authority.WithDelivery(context.Background())
	with := callMCP(t, server, body)
	withText, _ := json.Marshal(with)
	if strings.Contains(string(withText), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("trusted server ignored configured authority: %s", withText)
	}
	if strings.Contains(string(withText), "delivery") {
		t.Fatalf("serialized _meta influenced authority: %s", withText)
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
