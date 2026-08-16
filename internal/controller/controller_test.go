package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

func TestLogsReadsStructuredRuntimeSource(t *testing.T) {
	dir := t.TempDir()
	if err := runtime_log.New(dir).Append(runtime_log.Event{Timestamp: time.Now().UTC(), Level: "info", Component: "gateway", Event: "process_ready", Message: "ready"}); err != nil {
		t.Fatal(err)
	}
	c := Controller{Config: config.Config{StateDir: dir}}
	output, err := c.Logs("gateway", 10)
	if err != nil || !strings.Contains(output, `"event":"process_ready"`) {
		t.Fatalf("structured runtime logs output=%q err=%v", output, err)
	}
}

func TestReadGatewayLogDeltaIsBoundedAndSanitized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.log")
	data := strings.Repeat("padding\n", 3000) + "CONTROL_PLANE_API_KEY=do-not-leak\nfinal error\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	delta, truncated, err := (Controller{}).readGatewayLogDelta(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(delta) > 16<<10 {
		t.Fatalf("delta truncated=%v len=%d", truncated, len(delta))
	}
	if !strings.HasPrefix(delta, "padding\n") {
		t.Fatalf("truncated delta retained a partial first line: %q", delta[:min(len(delta), 32)])
	}
	if strings.Contains(delta, "do-not-leak") || !strings.Contains(delta, "final error") {
		t.Fatalf("unsanitized or incomplete delta: %q", delta)
	}
}

func TestRestartGatewayDiagnosticsExcludesPreviousShutdownOutput(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "gateway.log")
	if err := os.WriteFile(logPath, []byte("OLD_START\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Controller{Config: config.Config{ListenAddr: "127.0.0.1:18767", Controller: config.ControllerConfig{GatewayBinary: "/bin/true", PIDDir: filepath.Join(dir, "pid"), LogDir: logDir}}}
	appendLog := func(line string) error {
		f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(line + "\n")
		return err
	}
	oldStop, oldStart, oldWait := restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn
	defer func() { restartGatewayStopFn, restartGatewayStartFn, restartGatewayWaitFn = oldStop, oldStart, oldWait }()
	restartGatewayStopFn = func(Controller) error { return appendLog("OLD_SHUTDOWN") }
	restartGatewayStartFn = func(Controller) error {
		if err := appendLog("startup_phase=STATE_CHECK"); err != nil {
			return err
		}
		return appendLog("TARGET_FATAL")
	}
	restartGatewayWaitFn = func(string, bool, time.Duration) error { return fmt.Errorf("target readiness failed") }
	diagnostics, err := c.RestartGatewayAfterUpgradeDiagnostics()
	if err == nil {
		t.Fatal("target readiness failure was accepted")
	}
	if strings.Contains(diagnostics.LogDelta, "OLD_SHUTDOWN") || !strings.Contains(diagnostics.LogDelta, "TARGET_FATAL") {
		t.Fatalf("target log delta=%q", diagnostics.LogDelta)
	}
	if diagnostics.Phase != "STATE_CHECK" {
		t.Fatalf("startup phase = %q, want STATE_CHECK", diagnostics.Phase)
	}
}

func TestReadGatewayLogDeltaRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	path := filepath.Join(dir, "gateway.log")
	if err := os.WriteFile(target, []byte("error"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (Controller{}).readGatewayLogDelta(path, 0); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink read error=%v", err)
	}
}

func TestReadGatewayLogDeltaReportsMissingLog(t *testing.T) {
	if _, _, err := (Controller{}).readGatewayLogDelta(filepath.Join(t.TempDir(), "missing.log"), 0); err == nil {
		t.Fatal("missing log was reported as captured")
	}
}

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
