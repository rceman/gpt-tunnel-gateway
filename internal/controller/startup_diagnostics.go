package controller

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func (c Controller) RestartGatewayAfterUpgradeDiagnostics() (GatewayStartupDiagnostics, error) {
	c.processEvent("gateway", c.Config.Controller.GatewayBinary, "info", "restart_requested", 0, "gateway restart after upgrade requested", nil)
	started := time.Now()
	diagnostics := GatewayStartupDiagnostics{
		Phase:         "TARGET_STARTUP",
		CaptureStatus: "not_attempted",
	}
	lock, err := lockfile.Acquire(c.Config.Controller.PIDDir, "controller")
	if err != nil {
		diagnostics.Elapsed = time.Since(started)
		diagnostics.Error = err
		return diagnostics, err
	}
	defer lock.Release()
	logPath := c.logPath("gateway")
	var logOffset int64
	var logStatErr error
	captureReady := false
	capture := func() {
		if !captureReady {
			diagnostics.LogCaptureError = fmt.Errorf("target log baseline unavailable")
		} else if logStatErr != nil {
			diagnostics.LogCaptureError = logStatErr
		} else {
			var captureErr error
			diagnostics.LogDelta, diagnostics.LogDeltaTruncated, captureErr = c.readGatewayLogDelta(logPath, logOffset)
			if captureErr != nil {
				diagnostics.LogCaptureError = captureErr
			}
		}
		if diagnostics.LogCaptureError != nil {
			diagnostics.CaptureStatus = "failed"
		} else if diagnostics.ProcessStateError != nil {
			diagnostics.CaptureStatus = "partial"
		} else {
			diagnostics.CaptureStatus = "captured"
		}
	}
	failed := func(startErr error) (GatewayStartupDiagnostics, error) {
		diagnostics.Elapsed = time.Since(started)
		diagnostics.Error = startErr
		capture()
		return diagnostics, startErr
	}
	if err := restartGatewayStopFn(c); err != nil {
		return failed(err)
	}
	if info, statErr := os.Lstat(logPath); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			logStatErr = fmt.Errorf("target log baseline is not a regular file")
		} else {
			logOffset = info.Size()
		}
	} else if !os.IsNotExist(statErr) {
		logStatErr = statErr
	}
	captureReady = true
	if err := restartGatewayStartFn(c); err != nil {
		return failed(err)
	}
	if record, readErr := readPIDRecord(c.pidPath("gateway")); readErr == nil {
		diagnostics.TargetPID = record.PID
	}
	readyErr := restartGatewayWaitFn(c.gatewayReadyURL(), true, 30*time.Second)
	diagnostics.Elapsed = time.Since(started)
	diagnostics.ReadinessPassed = readyErr == nil
	expected, evalErr := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	if evalErr != nil {
		diagnostics.ProcessStateError = evalErr
	} else {
		status := c.process("gateway", expected)
		diagnostics.TargetProcessRunning = status.Running
		diagnostics.TargetProcessExited = !status.Running
		diagnostics.AliveButUnready = status.Running && readyErr != nil
	}
	diagnostics.Error = readyErr
	capture()
	return diagnostics, readyErr
}

func (c Controller) readGatewayLogDelta(path string, offset int64) (string, bool, error) {
	const maxBytes = 16 << 10
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("gateway log must not be a symlink")
	}
	if !linkInfo.Mode().IsRegular() {
		return "", false, fmt.Errorf("gateway log is not a regular file")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", false, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return "", false, fmt.Errorf("open gateway log")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("gateway log is not a regular file")
	}
	if offset < 0 || offset > info.Size() {
		offset = 0
	}
	truncated := false
	if info.Size()-offset > maxBytes {
		offset = info.Size() - maxBytes
		truncated = true
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", false, err
	}
	delta, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return "", false, err
	}
	if truncated {
		if newline := bytes.IndexByte(delta, '\n'); newline >= 0 {
			delta = delta[newline+1:]
		} else {
			delta = nil
		}
	}
	lines := strings.Split(string(delta), "\n")
	for i, line := range lines {
		lines[i] = sanitizeLogLine(line)
	}
	return strings.Join(lines, "\n"), truncated, nil
}

// StopGatewayForUpgrade stops only the gateway recorded by this controller.
func (c Controller) StopGatewayForUpgrade() error {
	return c.stopProcess("gateway", c.Config.Controller.GatewayBinary)
}
