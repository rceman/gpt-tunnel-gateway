package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestDaemonUnitIsCanonicalSystemGatewayAndTunnelService(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctlPath := filepath.Join(os.Getenv("PATH"), "gpt-tunnelctl")
	if err := os.WriteFile(ctlPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	envPath := filepath.Join(stateDir, "tunnel.env")
	if err := os.WriteFile(envPath, []byte("opaque=not-read-by-unit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(stateDir, "config.json")
	c := Controller{
		Config: config.Config{
			ListenAddr: "127.0.0.1:8765",
			StateDir:   stateDir,
			Controller: config.ControllerConfig{
				GatewayBinary:          "/opt/gpt-tunnel-gatewayd",
				TunnelClientBinary:     "/opt/gpt-tunnel",
				TunnelEnvFile:          envPath,
				TunnelHealthListenAddr: "127.0.0.1:8766",
			},
		},
		ConfigPath: configPath,
	}
	unit, err := c.daemonUnitText(daemonRuntimeIdentity{User: "therceman", Group: "therceman"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Type=oneshot\n",
		"RemainAfterExit=yes\n",
		"User=\"therceman\"\n",
		"Group=\"therceman\"\n",
		"Environment=GPT_TUNNEL_CONFIG=\"" + configPath + "\"\n",
		"ExecStart=\"" + ctlPath + "\" daemon-start\n",
		"ExecStop=\"" + ctlPath + "\" daemon-stop\n",
		"KillMode=control-group\n",
		"Restart=no\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
	for _, forbidden := range []string{"EnvironmentFile=", "ExecStartPost=", "ExecStart=\"/opt/gpt-tunnel-gatewayd\"", "ExecStart=\"/opt/gpt-tunnel\""} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("unit contains forbidden %q:\n%s", forbidden, unit)
		}
	}
}

func TestDaemonUnitUsesSystemPath(t *testing.T) {
	path, err := daemonUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/etc/systemd/system/gpt-tunnel.service" {
		t.Fatalf("unit path = %q", path)
	}
}
