package airelay

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
)

func (c Client) Status(ctx context.Context, session string) (SessionStatus, error) {
	if !sessionRE.MatchString(session) {
		return SessionStatus{}, fmt.Errorf("invalid Airelay session key")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 8192, 8192
	cmd := exec.CommandContext(ctx, c.Command, "session-status", session)
	cmd.Env = cleanEnv()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	status := parseSessionStatus(stdout.String())
	if cmd.ProcessState != nil {
		status.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		status.ExitCode = -1
	}
	if ctx.Err() != nil {
		return status, fmt.Errorf("Airelay session-status timeout: %w", ctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return status, fmt.Errorf("Airelay session-status output exceeds limit")
	}
	if err != nil {
		if status.ExitCode < 0 {
			return status, fmt.Errorf("Airelay session-status failed")
		}
		status.State = "error"
		status.Error = fmt.Sprintf("Airelay session-status exited with code %d", status.ExitCode)
	}
	return status, nil
}
func parseSessionStatus(output string) SessionStatus {
	status := SessionStatus{
		State:            "error",
		CapacityWarnings: []string{},
	}
	for _, raw := range strings.Split(normalizeTail(output), "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "controller:"):
			status.ControllerReachable = strings.Contains(lower, "reachable") && !strings.Contains(lower, "unreachable")
		case strings.HasPrefix(lower, "airelay version:"):
			status.AirelayVersion = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		case strings.HasPrefix(lower, "protocol version:"):
			status.ProtocolVersion = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		case strings.HasPrefix(lower, "state:"):
			status.State = normalizeSessionState(strings.TrimSpace(line[strings.Index(line, ":")+1:]))
		}
		if isCapacityWarning(lower) {
			status.CapacityWarnings = append(status.CapacityWarnings, line)
		}
	}
	return status
}
func normalizeSessionState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "busy", "running", "working", "active":
		return "running"
	case "waiting", "queued":
		return "waiting"
	case "free", "idle", "ready":
		return "idle"
	case "error", "failed", "unreachable", "stopped":
		return "error"
	default:
		return "error"
	}
}
func isCapacityWarning(lower string) bool {
	return strings.Contains(lower, "capacity") || strings.Contains(lower, "weekly limit") || strings.Contains(lower, "context") || strings.HasPrefix(lower, "⚠") || strings.Contains(lower, "warning")
}

var ansiRE = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

func normalizeTail(value string) string {
	value = ansiRE.ReplaceAllString(value, "")
	var b strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' || (!unicode.IsControl(r) && r != '\u007f') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func cleanEnv() []string {
	keys := []string{"HOME", "PATH", "USER", "LOGNAME", "TMPDIR"}
	out := []string{"LC_ALL=C"}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}
func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[truncated]"
}
