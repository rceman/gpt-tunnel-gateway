package controller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestDaemonUnitIsCanonicalSystemGatewayAndTunnelService(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "ctl dir")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	ctlPath := filepath.Join(os.Getenv("PATH"), "gpt-tunnelctl")
	if err := os.WriteFile(ctlPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state dir")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(stateDir, "tunnel.env")
	if err := os.WriteFile(envPath, []byte("opaque=not-read-by-unit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(stateDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
		"User=therceman\n",
		"Group=therceman\n",
		"Environment=GPT_TUNNEL_CONFIG=" + strings.ReplaceAll(configPath, " ", `\x20`) + "\n",
		"ExecStart=" + strings.ReplaceAll(ctlPath, " ", `\x20`) + " daemon-start\n",
		"ExecStop=" + strings.ReplaceAll(ctlPath, " ", `\x20`) + " daemon-stop\n",
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
	if strings.Contains(unit, `"`) {
		t.Fatalf("unit contains shell/JSON quoting:\n%s", unit)
	}
	for _, escaped := range []string{
		strings.ReplaceAll(workingDirectoryForTest(t, c), " ", `\x20`),
		strings.ReplaceAll(configPath, " ", `\x20`),
		strings.ReplaceAll(ctlPath, " ", `\x20`),
	} {
		if !strings.Contains(unit, escaped) {
			t.Fatalf("unit missing systemd-escaped value %q:\n%s", escaped, unit)
		}
	}
	if analyzer, err := exec.LookPath("systemd-analyze"); err == nil {
		unitPath := filepath.Join(t.TempDir(), "gpt-tunnel.service")
		if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(analyzer, "verify", unitPath).CombinedOutput()
		if err != nil {
			t.Fatalf("systemd-analyze verify failed: %v\n%s", err, output)
		}
	}
}

func workingDirectoryForTest(t *testing.T, c Controller) string {
	t.Helper()
	workingDir, err := c.gatewayWorkingDir()
	if err != nil {
		t.Fatal(err)
	}
	return workingDir
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
