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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{}}
		switch request.Method {
		case "initialize":
			response["result"] = map[string]any{"serverInfo": map[string]any{"version": "0.6.11"}}
		case "tools/list":
			tools := make([]any, 0, len(canonicalRuntimeTools))
			for _, name := range canonicalRuntimeTools {
				tools = append(tools, map[string]any{"name": name})
			}
			response["result"] = map[string]any{"tools": tools}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	if err := liveMCPSmoke(context.Background(), c, "0.6.11"); err != nil {
		t.Fatalf("live MCP smoke failed: %v", err)
	}
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
