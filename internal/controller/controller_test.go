package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

func TestProcessIdentityRejectsMismatchedPIDFile(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	dir := t.TempDir()
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "gateway.pid"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: dir, GatewayBinary: "/bin/false", TunnelClientBinary: "/bin/false"}}}
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Gateway.Running {
		t.Fatal("mismatched executable accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = ctx
	_ = os.ErrNotExist
}

func TestProcessIdentitySurvivesAtomicBinaryReplacement(t *testing.T) {
	if os.Getenv("GPT_TUNNEL_CONTROLLER_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "gpt-tunnel-gatewayd")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, data, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=TestProcessIdentitySurvivesAtomicBinaryReplacement", "--")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONTROLLER_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	pidPath := filepath.Join(dir, "gateway.pid")
	if err := fsutil.WriteFileAtomic(pidPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, binary); err != nil {
		t.Fatal(err)
	}

	c := Controller{Config: config.Config{Controller: config.ControllerConfig{PIDDir: dir, GatewayBinary: binary, TunnelClientBinary: binary}}}
	status := c.process("gateway", binary)
	if !status.Running || !status.IdentityValid || status.PID != cmd.Process.Pid {
		t.Fatalf("atomic replacement invalidated owned process: %#v", status)
	}
}
