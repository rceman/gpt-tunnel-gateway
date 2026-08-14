package airelay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var sessionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var provenanceRE = regexp.MustCompile(`^(?:S|SP|SD|SA|SW)-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}$`)

type Result struct {
	ExitCode   int       `json:"exit_code"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type TranscriptLine struct {
	Timestamp int64  `json:"timestamp"`
	Text      string `json:"text"`
}

type TranscriptResult struct {
	Lines      []TranscriptLine
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
}

type InterruptResult struct {
	Outcome   string `json:"outcome"`
	Requested bool   `json:"requested"`
	ElapsedMS int    `json:"elapsed_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	Reason    string `json:"reason,omitempty"`
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

func (c Client) Prompt(ctx context.Context, session, message string) (Result, error) {
	return c.PromptWithProvenance(ctx, session, "", message)
}

// PromptWithProvenance is the sole outbound text boundary. An empty origin
// denotes Gateway-owned automation; non-empty origins must be durable session
// IDs supplied by the server context, never by the caller's message.
func (c Client) PromptWithProvenance(ctx context.Context, session, origin, message string) (Result, error) {
	if !sessionRE.MatchString(session) {
		return Result{}, fmt.Errorf("invalid Airelay session key")
	}
	if origin == "" {
		origin = "GTW"
	} else if !provenanceRE.MatchString(origin) {
		return Result{}, fmt.Errorf("invalid durable session provenance")
	}
	if message == "" || strings.ContainsRune(message, 0) {
		return Result{}, fmt.Errorf("invalid Airelay message")
	}
	message = "[" + origin + "] " + message
	if len(message) > c.MaxMessageBytes {
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

// Interrupt delegates turn interruption to Airelay's controller. Gateway
// never writes PTY bytes or knows the harness-specific interrupt sequence.
func (c Client) Interrupt(ctx context.Context, session string) (InterruptResult, error) {
	if !sessionRE.MatchString(session) {
		return InterruptResult{}, fmt.Errorf("invalid Airelay session key")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 8192, 8192
	cmd := exec.CommandContext(ctx, c.Command, "interrupt", session, "--json", "--no-warn")
	cmd.Env = cleanEnv()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return InterruptResult{
			Outcome:   "timed_out",
			Requested: true,
		}, fmt.Errorf("Airelay interrupt timeout: %w", ctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return InterruptResult{
			Outcome:   "failed",
			Requested: false,
		}, fmt.Errorf("Airelay interrupt output exceeds limit")
	}
	var result InterruptResult
	if decodeErr := json.Unmarshal([]byte(normalizeTail(stdout.String())), &result); decodeErr != nil || result.Outcome == "" {
		if err != nil {
			return InterruptResult{
				Outcome:   "failed",
				Requested: false,
			}, fmt.Errorf("Airelay interrupt failed: %w", err)
		}
		return InterruptResult{
			Outcome:   "failed",
			Requested: false,
		}, fmt.Errorf("Airelay interrupt returned invalid result")
	}
	if result.Outcome == "no_active_turn" {
		result.Outcome = "already_idle"
	}
	return result, nil
}

func (c Client) Tail(ctx context.Context, session string, lines int) (Result, error) {
	return c.tail(ctx, session, lines, false)
}

// TailSnapshot reads the bounded session output window and permits an empty
// window so callers can return a successful empty incremental delta.
func (c Client) TailSnapshot(ctx context.Context, session string, lines int) (Result, error) {
	return c.tail(ctx, session, lines, true)
}

func (c Client) tail(ctx context.Context, session string, lines int, allowEmpty bool) (Result, error) {
	if !sessionRE.MatchString(session) {
		return Result{}, fmt.Errorf("invalid Airelay session key")
	}
	if lines < 1 || lines > 200 {
		return Result{}, fmt.Errorf("invalid Airelay tail line count")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	result := Result{StartedAt: time.Now().UTC()}
	cmd := exec.CommandContext(ctx, c.Command, "tail", session, "--lines", fmt.Sprintf("%d", lines))
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
	if !allowEmpty && strings.TrimSpace(result.Stdout) == "" {
		return result, fmt.Errorf("Airelay tail returned no output")
	}
	if err != nil {
		return result, fmt.Errorf("Airelay tail failed")
	}
	return result, nil
}

// TailWithSkip returns an older bounded window by asking Airelay for the
// requested window plus the newest lines to skip. It never performs a second
// request or retries a failed call.
func (c Client) TailWithSkip(ctx context.Context, session string, lines, skip int) (Result, error) {
	if lines < 1 || lines > 200 || skip < 0 || lines+skip > 200 {
		return Result{}, fmt.Errorf("invalid Airelay tail bounds")
	}
	result, err := c.Tail(ctx, session, lines+skip)
	if err != nil || skip == 0 {
		return result, err
	}
	parts := strings.Split(strings.TrimRight(result.Stdout, "\r\n"), "\n")
	if len(parts) <= skip {
		return result, fmt.Errorf("Airelay tail skip exceeds available output")
	}
	result.Stdout = strings.Join(parts[:len(parts)-skip], "\n") + "\n"
	return result, nil
}

// Transcript reads a bounded, oldest-to-newest window from Airelay's
// persisted transcript. It is intentionally separate from Tail, which reads
// only the live viewport.
func (c Client) Transcript(ctx context.Context, session string, lines int) (TranscriptResult, error) {
	if !sessionRE.MatchString(session) {
		return TranscriptResult{}, fmt.Errorf("invalid Airelay session key")
	}
	if lines < 1 || lines > 200 {
		return TranscriptResult{}, fmt.Errorf("invalid Airelay transcript line count")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	result := TranscriptResult{StartedAt: time.Now().UTC()}
	cmd := exec.CommandContext(ctx, c.Command, "transcript", session, "--lines", fmt.Sprintf("%d", lines), "--order", "asc", "--json")
	cmd.Env = cleanEnv()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 64*1024, 8192
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.FinishedAt = time.Now().UTC()
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
	var payload struct {
		Lines []TranscriptLine `json:"lines"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &payload); err != nil {
		return result, fmt.Errorf("Airelay transcript returned invalid JSON")
	}
	if len(payload.Lines) > lines {
		return result, fmt.Errorf("Airelay transcript exceeded requested bound")
	}
	for i := range payload.Lines {
		payload.Lines[i].Text = normalizeTail(payload.Lines[i].Text)
	}
	result.Lines = payload.Lines
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
