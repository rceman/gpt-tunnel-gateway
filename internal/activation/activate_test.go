package activation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
)

func TestBoundedOutputIsDeterministicAndLimited(t *testing.T) {
	if got := BoundedOutput([]byte("  activation ok  \n")); got != "activation ok" {
		t.Fatalf("unexpected bounded output %q", got)
	}
	if got := BoundedOutput([]byte(strings.Repeat("x", OutputLimit+10))); len(got) != OutputLimit {
		t.Fatalf("output was not bounded: %d", len(got))
	}
}

func TestBoundedDiagnosticOutputReportsTruncation(t *testing.T) {
	value, truncated := BoundedDiagnosticOutput([]byte(strings.Repeat("x", DiagnosticOutputLimit+1)))
	if !truncated || len(value) != DiagnosticOutputLimit {
		t.Fatalf("diagnostic output bound=%d truncated=%v", len(value), truncated)
	}
}

func TestLiveMCPSmokeUsesCanonicalToolManifest(t *testing.T) {
	server := manifestSmokeServer(t, canonicalRuntimeTools)
	defer server.Close()
	c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	if err := liveMCPSmoke(context.Background(), c, "0.6.11"); err != nil {
		t.Fatalf("live MCP smoke failed: %v", err)
	}
}

func TestLiveMCPSmokeRejectsManifestDrift(t *testing.T) {
	for _, manifest := range [][]string{
		{"bootstrap", "call", "project_onboard", "schema", "session_start", "session_update"},
		{"batch", "call", "schema", "session_start"},
	} {
		server := manifestSmokeServer(t, manifest)
		c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}
		if err := liveMCPSmoke(context.Background(), c, "0.6.11"); err == nil {
			t.Fatalf("manifest %v was accepted", manifest)
		}
		server.Close()
	}
}

func manifestSmokeServer(t *testing.T, manifest []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{}}
		if request.Method == "initialize" {
			response["result"] = map[string]any{"serverInfo": map[string]any{"version": "0.6.11"}}
		} else if request.Method == "tools/list" {
			tools := make([]any, 0, len(manifest))
			for _, name := range manifest {
				tools = append(tools, map[string]any{"name": name})
			}
			response["result"] = map[string]any{"tools": tools}
		} else if request.Method == "tools/call" {
			response["result"] = map[string]any{"structuredContent": map[string]any{"ready": true, "gateways": []any{map[string]any{"key": "test", "ready": true}}, "captured_at": "2026-08-30T00:00:00Z"}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}

func TestControlReleasePathsExcludeTunnelProcess(t *testing.T) {
	paths := releaseartifacts.Paths("/opt/gpt-tunnel-gatewayd")
	if len(paths) != len(releaseartifacts.BinaryNames) {
		t.Fatalf("control release path count = %d, want %d", len(paths), len(releaseartifacts.BinaryNames))
	}
	for _, name := range []string{"airelay", "gpt-tunnel-client"} {
		if _, ok := paths[name]; ok {
			t.Fatalf("control release unexpectedly includes tunnel process %q", name)
		}
	}
}
