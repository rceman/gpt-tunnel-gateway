package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestRuntimeLogsIsBoundedReadOnlyGenericAction(t *testing.T) {
	server := &Server{Service: service.New(config.Config{StateDir: t.TempDir()})}
	entry, ok := server.genericActionRegistry(server.tools())["runtime/logs"]
	if !ok || entry.Execute == nil {
		t.Fatal("runtime/logs action was not registered")
	}
	if !entry.Annotations.ReadOnlyHint || entry.AuthorityRole != "" {
		t.Fatalf("runtime/logs authority/annotations = %#v/%q", entry.Annotations, entry.AuthorityRole)
	}
	properties := entry.InputSchema["properties"].(map[string]any)
	if len(properties) != 10 || entry.InputSchema["additionalProperties"] != false {
		t.Fatalf("runtime/logs input schema is not bounded/closed: %#v", entry.InputSchema)
	}
	if _, ok := properties["path"]; ok {
		t.Fatal("runtime/logs exposed an arbitrary path")
	}
	if _, ok := server.publicTools()["runtime/logs"]; ok {
		t.Fatal("runtime/logs became a top-level MCP tool")
	}
}

func TestRuntimeRestartPublicCallReturnsBeforeDeferredWorker(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithDelivery(context.Background())
	sessionID := genericSessionWithRole(t, server.Service, "example", durableSession.RoleDelivery)
	oldAccept := gatewayRecoveryAcceptFn
	defer func() { gatewayRecoveryAcceptFn = oldAccept }()
	var responseReturned atomic.Bool
	workerAfterResponse := make(chan struct{}, 1)
	gatewayRecoveryAcceptFn = func(_ controller.Controller, operationID string, release func(func())) (controller.GatewayRecoveryResult, error) {
		release(func() {
			if !responseReturned.Load() {
				t.Error("recovery worker ran before the HTTP response was returned")
			}
			workerAfterResponse <- struct{}{}
		})
		return controller.GatewayRecoveryResult{OperationID: operationID, Outcome: "accepted"}, nil
	}
	body := mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "runtime/restart", "input": map[string]any{"operation_id": "restart-http"},
		}},
	})
	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(body))
	request.Host = "127.0.0.1:1"
	request.RemoteAddr = "127.0.0.1:1234"
	server.Router().ServeHTTP(record, request)
	responseReturned.Store(true)
	if record.Code != http.StatusOK {
		t.Fatalf("runtime/restart HTTP status=%d body=%s", record.Code, record.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(record.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("runtime/restart response=%#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["result"].(map[string]any)["outcome"] != "accepted" {
		t.Fatalf("runtime/restart did not return accepted receipt: %#v", response)
	}
	select {
	case <-workerAfterResponse:
	case <-time.After(time.Second):
		t.Fatal("deferred recovery worker was not released")
	}
}
