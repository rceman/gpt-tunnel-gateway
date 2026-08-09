package upgrade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestSmokeTimeoutIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	addr := strings.TrimPrefix(server.URL, "http://")
	c := config.Config{ListenAddr: addr, GatewayID: "home", StateDir: t.TempDir(), Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
	start := time.Now()
	if err := smoke(ctx, c, "0.2.3", "0.2.2"); err == nil {
		t.Fatal("timeout server accepted")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("smoke exceeded timeout bound")
	}
}

func TestSmokeRejectsMalformedJSONRPCAndToolContracts(t *testing.T) {
	validInit := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","serverInfo":{"version":"0.2.3"}}}`
	cases := []struct{ name, body string }{
		{"jsonrpc-version", `{"jsonrpc":"1.0","id":1,"result":{}}`},
		{"mismatched-id", `{"jsonrpc":"2.0","id":9,"result":{}}`},
		{"top-level-error", `{"jsonrpc":"2.0","id":1,"error":{"code":-1}}`},
		{"missing-result", `{"jsonrpc":"2.0","id":1}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://"), GatewayID: "home", StateDir: t.TempDir(), Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
			if err := smoke(context.Background(), c, "0.2.3", "0.2.2"); err == nil {
				t.Fatal("malformed response accepted")
			}
		})
	}
	validTool := map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}}
	validList := map[string]any{"tools": []any{validTool}}
	contractCases := []struct {
		name                       string
		list                       map[string]any
		pingError, capabilityError bool
	}{
		{"malformed-tool-descriptor", map[string]any{"tools": []any{"bad"}}, false, false},
		{"missing-tool-name", map[string]any{"tools": []any{map[string]any{"inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": validTool["annotations"]}}}, false, false},
		{"empty-tool-name", map[string]any{"tools": []any{map[string]any{"name": "", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": validTool["annotations"]}}}, false, false},
		{"invalid-input-schema", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "array"}, "outputSchema": map[string]any{"type": "object"}, "annotations": validTool["annotations"]}}}, false, false},
		{"invalid-output-schema", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "array"}, "annotations": validTool["annotations"]}}}, false, false},
		{"invalid-annotations", map[string]any{"tools": []any{map[string]any{"name": "system_ping", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": "yes"}}}}, false, false},
		{"ping-error-result", validList, true, false},
		{"capability-error-result", validList, false, true},
	}
	for _, test := range contractCases {
		t.Run(test.name, func(t *testing.T) {
			count := 0
			stateDir := t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				body := validInit
				if count == 2 {
					payload := test.list
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "result": payload})
					body = string(bodyBytes)
				} else if count == 3 {
					ping := map[string]any{"isError": false, "structuredContent": map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.2.3", "gateway_id": "home"}}
					if test.pingError {
						ping["isError"] = true
					}
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 3, "result": ping})
					body = string(bodyBytes)
				} else if count == 4 {
					capability := map[string]any{"isError": false, "structuredContent": map[string]any{"gateway_id": "home", "hub_protocol_root": "gpt-tunnel/v1", "hub_branch": "gpt-tunnel/home", "hub_managed_root": filepath.Join(stateDir, "hub", "repository")}}
					if test.capabilityError {
						capability["isError"] = true
					}
					bodyBytes, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 4, "result": capability})
					body = string(bodyBytes)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://"), GatewayID: "home", StateDir: stateDir, Hub: config.HubConfig{Branch: "gpt-tunnel/home"}}
			if err := smoke(context.Background(), c, "0.2.3", "0.2.2"); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
}
