package controller

import (
	"context"
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
