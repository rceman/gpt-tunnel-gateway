package airelay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var sessionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

const nativeViewportRows = 30

type Result struct {
	ExitCode   int       `json:"exit_code"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type SessionStatus struct {
	State               string   `json:"state"`
	ControllerReachable bool     `json:"controller_reachable"`
	AirelayVersion      string   `json:"airelay_version,omitempty"`
	ProtocolVersion     string   `json:"protocol_version,omitempty"`
	CapacityWarnings    []string `json:"capacity_warnings"`
	ExitCode            int      `json:"exit_code"`
	Error               string   `json:"error,omitempty"`
}

type Client struct {
	Command         string
	Timeout         time.Duration
	MaxMessageBytes int
}

type tailBuffer struct {
	bytes.Buffer
	max      int
	exceeded bool
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		remaining := b.max - b.Len()
		if remaining > 0 {
			_, _ = b.Buffer.Write(p[:remaining])
		}
		b.exceeded = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func (b *tailBuffer) ReadFrom(r io.Reader) (int64, error) {
	var buf [32 * 1024]byte
	var total int64
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			_, _ = b.Write(buf[:n])
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func (c Client) Prompt(ctx context.Context, session, message string) (Result, error) {
	if !sessionRE.MatchString(session) {
		return Result{}, fmt.Errorf("invalid Airelay session key")
	}
	if message == "" || len(message) > c.MaxMessageBytes || strings.ContainsRune(message, 0) {
		return Result{}, fmt.Errorf("invalid Airelay message")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	result := Result{StartedAt: time.Now().UTC()}
	cmd := exec.CommandContext(ctx, c.Command, "prompt", session, message)
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.FinishedAt = time.Now().UTC()
	result.Stdout = bounded(stdout.String(), 8192)
	result.Stderr = bounded(stderr.String(), 8192)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("Airelay prompt timeout: %w", ctx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("Airelay prompt failed: %w", err)
	}
	return result, nil
}

func (c Client) Tail(ctx context.Context, session string, lines int) (Result, error) {
	if !sessionRE.MatchString(session) {
		return Result{}, fmt.Errorf("invalid Airelay session key")
	}
	if lines != -1 && (lines < 1 || lines > 30) {
		return Result{}, fmt.Errorf("invalid Airelay tail line count")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	result := Result{StartedAt: time.Now().UTC()}
	args := []string{"tail", session}
	if lines == -1 {
		lines = nativeViewportRows
	}
	args = append(args, "--lines", fmt.Sprintf("%d", lines))
	cmd := exec.CommandContext(ctx, c.Command, args...)
	cmd.Env = cleanEnv()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 8192, 8192
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.FinishedAt = time.Now().UTC()
	result.Stdout = normalizeTail(stdout.String())
	result.Stderr = bounded(stderr.String(), 8192)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("Airelay tail timeout: %w", ctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return result, fmt.Errorf("Airelay tail output exceeds limit")
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return result, fmt.Errorf("Airelay tail returned no output")
	}
	if err != nil {
		return result, fmt.Errorf("Airelay tail failed")
	}
	return result, nil
}

// Transcript reads a bounded window from Airelay's retained transcript. The
// native skip operation keeps large histories out of the gateway process.
func (c Client) Transcript(ctx context.Context, session string, lines, skip int) (Result, error) {
	if !sessionRE.MatchString(session) {
		return Result{}, fmt.Errorf("invalid Airelay session key")
	}
	if lines < 1 || lines > 50 || skip < 0 {
		return Result{}, fmt.Errorf("invalid Airelay transcript bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	result := Result{StartedAt: time.Now().UTC()}
	cmd := exec.CommandContext(ctx, c.Command, "transcript", session, "--lines", strconv.Itoa(lines), "--skip", strconv.Itoa(skip), "--order", "desc")
	cmd.Env = cleanEnv()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 8192, 8192
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.FinishedAt = time.Now().UTC()
	result.Stdout = normalizeTail(stdout.String())
	result.Stderr = bounded(stderr.String(), 8192)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("Airelay transcript timeout: %w", ctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return result, fmt.Errorf("Airelay transcript output exceeds limit")
	}
	if err != nil {
		return result, fmt.Errorf("Airelay transcript failed")
	}
	return result, nil
}

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
	status := SessionStatus{State: "error", CapacityWarnings: []string{}}
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
	case "idle", "ready":
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
