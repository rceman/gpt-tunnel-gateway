package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

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

func TestRuntimeRestartResponseBoundaryFlushesBeforeWorker(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithDelivery(context.Background())
	sessionID := genericSessionWithRole(t, server.Service, "example", durableSession.RoleDelivery)
	oldAccept := gatewayRecoveryAcceptFn
	defer func() { gatewayRecoveryAcceptFn = oldAccept }()
	record := &responseBoundaryRecorder{ResponseRecorder: httptest.NewRecorder()}
	var workerAfterFlush atomic.Bool
	gatewayRecoveryAcceptFn = func(_ controller.Controller, operationID string, release func(func())) (controller.GatewayRecoveryResult, error) {
		release(func() {
			if record.flushes != 1 {
				t.Errorf("recovery worker ran before the response was flushed: flushes=%d", record.flushes)
			}
			workerAfterFlush.Store(true)
		})
		return controller.GatewayRecoveryResult{OperationID: operationID, Outcome: "accepted"}, nil
	}
	body := mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "runtime/restart", "input": map[string]any{"operation_id": "restart-http"},
		}},
	})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(body))
	request.Host = "127.0.0.1:1"
	request.RemoteAddr = "127.0.0.1:1234"
	server.Router().ServeHTTP(record, request)
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
	if !workerAfterFlush.Load() {
		t.Fatal("deferred recovery worker was not released")
	}
}

func TestRuntimeRestartNetworkReturnsStableReceipt(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithDelivery(context.Background())
	sessionID := genericSessionWithRole(t, server.Service, "example", durableSession.RoleDelivery)
	oldAccept := gatewayRecoveryAcceptFn
	defer func() { gatewayRecoveryAcceptFn = oldAccept }()
	var workerRuns atomic.Int32
	var schedule sync.Once
	gatewayRecoveryAcceptFn = func(_ controller.Controller, operationID string, release func(func())) (controller.GatewayRecoveryResult, error) {
		schedule.Do(func() { release(func() { workerRuns.Add(1) }) })
		return controller.GatewayRecoveryResult{OperationID: operationID, Outcome: "accepted", TunnelPID: 9123}, nil
	}
	httpServer := httptest.NewServer(server.Router())
	defer httpServer.Close()
	client := &frozenConnectorClient{http: httpServer.Client(), endpoint: httpServer.URL + "/mcp", methods: map[string]int{}}
	response := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": "runtime/restart", "input": map[string]any{"operation_id": "restart-network"}},
	}))
	result, ok := response["result"].(map[string]any)
	if !ok || result["outcome"] != "accepted" || result["tunnel_pid"] != float64(9123) {
		t.Fatalf("network runtime/restart result=%#v", response)
	}
	if workerRuns.Load() != 1 {
		t.Fatalf("network runtime/restart worker runs=%d, want 1", workerRuns.Load())
	}
	second := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": "runtime/restart", "input": map[string]any{"operation_id": "restart-network"}},
	}))
	secondResult, ok := second["result"].(map[string]any)
	if !ok || secondResult["outcome"] != "accepted" {
		t.Fatalf("duplicate network runtime/restart result=%#v", second)
	}
	if workerRuns.Load() != 1 {
		t.Fatalf("duplicate network runtime/restart worker runs=%d, want 1", workerRuns.Load())
	}
}
