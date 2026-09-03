package activation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
)

func TestRunBoundedCommandCapsCombinedOutput(t *testing.T) {
	if os.Getenv("GTW_ACTIVATION_OUTPUT_HELPER") == "1" {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, 20000))
		return
	}
	command := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestRunBoundedCommandCapsCombinedOutput$", "-test.v=false")
	command.Env = append(os.Environ(), "GTW_ACTIVATION_OUTPUT_HELPER=1")
	output, err := runBoundedCommand(command)
	if err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("expected bounded-output error, got %v", err)
	}
	if len(output) != activationSubprocessOutputLimit {
		t.Fatalf("captured output length = %d, want %d", len(output), activationSubprocessOutputLimit)
	}
}

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
	if err := LiveMCPSmoke(context.Background(), c, "0.6.14"); err != nil {
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
		if err := LiveMCPSmoke(context.Background(), c, "0.6.14"); err == nil {
			t.Fatalf("manifest %v was accepted", manifest)
		}
		server.Close()
	}
}

func manifestSmokeServer(t *testing.T, manifest []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}}
		if request.Method == "initialize" {
			response["result"] = map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]any{"version": "0.6.14"}}
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

func TestLiveMCPSmokeRetainsJSONRPCErrorContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]any{"version": "0.6.14"}}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{
				map[string]any{"name": "status"}, map[string]any{"name": "guide"}, map[string]any{"name": "projects"},
				map[string]any{"name": "session_start"}, map[string]any{"name": "schema"}, map[string]any{"name": "call"},
			}}
		case "tools/call":
			response["error"] = map[string]any{"code": -32601, "message": "status unavailable"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	err := LiveMCPSmoke(context.Background(), c, "0.6.14")
	if err == nil || !strings.Contains(err.Error(), "tools/call") || !strings.Contains(err.Error(), "-32601") || !strings.Contains(err.Error(), "status unavailable") {
		t.Fatalf("MCP error context = %v", err)
	}
}

func TestLiveMCPSmokeHonorsBoundedContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	started := time.Now()
	if err := LiveMCPSmoke(ctx, c, "0.6.14"); err == nil {
		t.Fatal("slow MCP server was accepted")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("canonical MCP smoke exceeded its bounded context")
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
