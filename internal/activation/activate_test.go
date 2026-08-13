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

func TestLiveMCPSmokeUsesCanonicalStatusTool(t *testing.T) {
	var toolName string
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
		case "tools/call":
			toolName, _ = request.Params["name"].(string)
			if toolName == "system_ping" {
				response["error"] = map[string]any{"message": "legacy MCP alias is not supported"}
				delete(response, "result")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	if err := liveMCPSmoke(context.Background(), c, "0.6.11"); err != nil {
		t.Fatalf("live MCP smoke failed: %v", err)
	}
	if toolName != "status" {
		t.Fatalf("live MCP smoke called %q, want canonical status", toolName)
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
