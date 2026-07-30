package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestReadTunnelEnvRequiresSecretsAndRejectsControllerBindings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnel.env")
	valid := "CONTROL_PLANE_API_KEY=secret\nCONTROL_PLANE_TUNNEL_ID=tunnel_0123456789abcdef0123456789abcdef\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := readTunnelEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(values, "\n") != "CONTROL_PLANE_API_KEY=secret\nCONTROL_PLANE_TUNNEL_ID=tunnel_0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected env: %#v", values)
	}
	if err := os.WriteFile(path, []byte(valid+"MCP_SERVER_URL=http://wrong/mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTunnelEnv(path); err == nil {
		t.Fatal("controller-owned MCP binding accepted")
	}
}

func TestReadTunnelEnvRejectsOpenPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.env")
	if err := os.WriteFile(path, []byte("CONTROL_PLANE_API_KEY=x\nCONTROL_PLANE_TUNNEL_ID=y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTunnelEnv(path); err == nil {
		t.Fatal("open tunnel env permissions accepted")
	}
}

func TestProcessEnvDoesNotInheritUnrelatedBindings(t *testing.T) {
	t.Setenv("MCP_SERVER_URL", "http://wrong/mcp")
	env := processEnv([]string{"MCP_SERVER_URL=http://127.0.0.1:8875/mcp"})
	joined := strings.Join(env, "\n")
	if strings.Count(joined, "MCP_SERVER_URL=") != 1 || !strings.Contains(joined, "MCP_SERVER_URL=http://127.0.0.1:8875/mcp") {
		t.Fatalf("unexpected process env: %s", joined)
	}
}

func TestReadTunnelEnvRejectsInvalidTunnelID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.env")
	if err := os.WriteFile(path, []byte("CONTROL_PLANE_API_KEY=x\nCONTROL_PLANE_TUNNEL_ID=not-a-tunnel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTunnelEnv(path); err == nil {
		t.Fatal("invalid tunnel id accepted")
	}
}

func TestControllerDerivesCanonicalReadyURLs(t *testing.T) {
	c := Controller{Config: config.Config{ListenAddr: "127.0.0.1:8875", Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8766"}}}
	if c.gatewayReadyURL() != "http://127.0.0.1:8875/readyz" || c.tunnelReadyURL() != "http://127.0.0.1:8766/readyz" {
		t.Fatalf("unexpected ready URLs: %s %s", c.gatewayReadyURL(), c.tunnelReadyURL())
	}
}
