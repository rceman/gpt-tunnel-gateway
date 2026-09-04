package airelay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

// ExecutionSessionRequest identifies one server-owned execution lane. The
// caller supplies no user-controlled path; WorktreePath is resolved by GTW.
type ExecutionSessionRequest struct {
	BaseSessionKey string
	Profile        string
	WorktreePath   string
	Identity       string
	LockDir        string
}

type executionSessionEntry struct {
	ID         string `json:"sessionId"`
	Profile    string `json:"profile"`
	SessionKey string `json:"sessionKey"`
	CWD        string `json:"cwd"`
	Controller string `json:"controllerEndpoint"`
}

type launchHistoryEntry struct {
	Profile       string `json:"profile"`
	SessionKey    string `json:"sessionKey"`
	InvocationCWD string `json:"invocationCwd"`
}

var profileRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type SessionAuthority struct {
	SessionKey          string `json:"sessionKey"`
	Profile             string `json:"profile"`
	ControllerReachable bool   `json:"controllerReachable"`
	State               string `json:"state"`
}

// DeriveExecutionSessionKey is the pure, server-owned lane key derivation.
// Callers must resolve the profile from canonical Agent authority; this
// function never infers it from an arbitrary active session.
func DeriveExecutionSessionKey(baseSessionKey, profile, identity string) (string, error) {
	if !sessionRE.MatchString(baseSessionKey) || !profileRE.MatchString(profile) || identity == "" || strings.ContainsAny(identity, "\x00\r\n") {
		return "", fmt.Errorf("invalid execution session authority")
	}
	digest := sha256.Sum256([]byte(baseSessionKey + "\x00" + profile + "\x00" + identity))
	return "gtw_lane_" + hex.EncodeToString(digest[:])[:32], nil
}

// EnsureExecutionSession returns a deterministic lane-specific session key.
// Existing sessions are reused only after exact machine-readable authority
// checks. A mismatched session is never retargeted or restarted.
func (c Client) EnsureExecutionSession(ctx context.Context, in ExecutionSessionRequest) (string, error) {
	if !sessionRE.MatchString(in.BaseSessionKey) || in.WorktreePath == "" || filepath.Clean(in.WorktreePath) != in.WorktreePath || !filepath.IsAbs(in.WorktreePath) || in.LockDir == "" || !filepath.IsAbs(in.LockDir) {
		return "", fmt.Errorf("invalid execution session authority")
	}
	worktree, err := filepath.Abs(in.WorktreePath)
	if err != nil {
		return "", fmt.Errorf("resolve execution worktree: %w", err)
	}
	profile := strings.TrimSpace(in.Profile)
	if profile == "" || !profileRE.MatchString(profile) {
		return "", fmt.Errorf("execution session profile is unavailable")
	}
	key, err := DeriveExecutionSessionKey(in.BaseSessionKey, profile, in.Identity)
	if err != nil {
		return "", err
	}
	lockName := "airelay-execution-" + key
	lock, err := lockfile.Acquire(in.LockDir, lockName)
	if err != nil {
		return "", fmt.Errorf("serialize execution Airelay session %q: %w", key, err)
	}
	defer func() { _ = lock.Release() }()
	sessions, err := c.executionSessions(ctx)
	if err != nil {
		return "", err
	}

	matched := 0
	for _, entry := range sessions {
		if entry.SessionKey != key {
			continue
		}
		if entry.Profile != profile || normalizeCWD(entry.CWD) != worktree {
			return "", fmt.Errorf("execution Airelay session %q has mismatched authority", key)
		}
		matched++
	}
	if matched > 1 {
		return "", fmt.Errorf("execution Airelay session %q is ambiguous", key)
	}
	history, err := c.executionHistory(ctx)
	if err != nil {
		return "", err
	}
	historyMatch := 0
	for _, entry := range history {
		if entry.SessionKey != key {
			continue
		}
		if entry.Profile != profile || normalizeCWD(entry.InvocationCWD) != worktree {
			return "", fmt.Errorf("execution Airelay launch history %q has mismatched authority", key)
		}
		historyMatch++
	}
	if matched == 1 && historyMatch == 0 {
		return "", fmt.Errorf("execution Airelay session %q has incomplete launch authority", key)
	}
	if matched == 0 {
		launchCtx, cancel := context.WithTimeout(ctx, c.Timeout)
		defer cancel()
		cmd := exec.CommandContext(launchCtx, c.Command, "start", profile, "--key", key, "--detached")
		cmd.Dir = worktree
		cmd.Env = cleanEnv()
		var stdout, stderr tailBuffer
		stdout.max, stderr.max = 8192, 8192
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("start execution Airelay session: %w", err)
		}
		if stdout.exceeded || stderr.exceeded {
			return "", fmt.Errorf("execution Airelay launch output exceeds limit")
		}
		sessions, err = c.executionSessions(ctx)
		if err != nil {
			return "", err
		}
		history, err = c.executionHistory(ctx)
		if err != nil {
			return "", err
		}
		matched, historyMatch = 0, 0
		for _, entry := range sessions {
			if entry.SessionKey == key {
				if entry.Profile != profile || normalizeCWD(entry.CWD) != worktree {
					return "", fmt.Errorf("new execution Airelay session %q has mismatched authority", key)
				}
				matched++
			}
		}
		for _, entry := range history {
			if entry.SessionKey == key {
				if entry.Profile != profile || normalizeCWD(entry.InvocationCWD) != worktree {
					return "", fmt.Errorf("new execution Airelay history %q has mismatched authority", key)
				}
				historyMatch++
			}
		}
		if matched != 1 || historyMatch == 0 {
			return "", fmt.Errorf("execution Airelay launch %q was not durably recorded", key)
		}
	}
	status, err := c.executionStatus(ctx, key)
	if err != nil || status.Profile != profile || !status.ControllerReachable || status.State == "error" {
		if err != nil {
			return "", fmt.Errorf("execution Airelay session is not reachable: %w", err)
		}
		return "", fmt.Errorf("execution Airelay session is not reachable")
	}
	return key, nil
}

// ValidateExecutionSession verifies an existing lane session without
// launching, relaunching, or retargeting anything. It is required for an
// already-running Train Attempt.
func (c Client) ValidateExecutionSession(ctx context.Context, in ExecutionSessionRequest) error {
	if !sessionRE.MatchString(in.BaseSessionKey) || in.WorktreePath == "" || filepath.Clean(in.WorktreePath) != in.WorktreePath || !filepath.IsAbs(in.WorktreePath) {
		return fmt.Errorf("invalid execution session authority")
	}
	profile := strings.TrimSpace(in.Profile)
	if profile == "" || !profileRE.MatchString(profile) {
		return fmt.Errorf("execution session profile is unavailable")
	}
	worktree, err := filepath.Abs(in.WorktreePath)
	if err != nil {
		return fmt.Errorf("resolve execution worktree: %w", err)
	}
	key, err := DeriveExecutionSessionKey(in.BaseSessionKey, profile, in.Identity)
	if err != nil {
		return err
	}
	sessions, err := c.executionSessions(ctx)
	if err != nil {
		return err
	}
	matched := 0
	for _, entry := range sessions {
		if entry.SessionKey != key {
			continue
		}
		if entry.Profile != profile || normalizeCWD(entry.CWD) != worktree {
			return fmt.Errorf("execution Airelay session %q has mismatched authority", key)
		}
		matched++
	}
	if matched != 1 {
		return fmt.Errorf("execution Airelay session %q is not exactly active", key)
	}
	history, err := c.executionHistory(ctx)
	if err != nil {
		return err
	}
	historyMatch := 0
	for _, entry := range history {
		if entry.SessionKey != key {
			continue
		}
		if entry.Profile != profile || normalizeCWD(entry.InvocationCWD) != worktree {
			return fmt.Errorf("execution Airelay launch history %q has mismatched authority", key)
		}
		historyMatch++
	}
	if historyMatch == 0 {
		return fmt.Errorf("execution Airelay launch history %q is not exactly recorded", key)
	}
	status, err := c.executionStatus(ctx, key)
	if err != nil || status.Profile != profile || !status.ControllerReachable || status.State == "error" {
		if err != nil {
			return fmt.Errorf("execution Airelay session is not reachable: %w", err)
		}
		return fmt.Errorf("execution Airelay session is not reachable")
	}
	return nil
}

func (c Client) executionSessions(ctx context.Context) ([]executionSessionEntry, error) {
	var rows []executionSessionEntry
	if err := c.runJSON(ctx, []string{"sessions", "--active", "--json"}, &rows); err != nil {
		return nil, fmt.Errorf("read Airelay sessions: %w", err)
	}
	return rows, nil
}

func (c Client) executionHistory(ctx context.Context) ([]launchHistoryEntry, error) {
	var entries []launchHistoryEntry
	if err := c.runJSON(ctx, []string{"history", "--all", "--json"}, &entries); err != nil {
		return nil, fmt.Errorf("read Airelay launch history: %w", err)
	}
	return entries, nil
}

func (c Client) executionStatus(ctx context.Context, key string) (SessionAuthority, error) {
	var status SessionAuthority
	if err := c.runJSON(ctx, []string{"session-status", key, "--json", "--no-warn"}, &status); err != nil {
		return SessionAuthority{}, err
	}
	if status.SessionKey != key || !profileRE.MatchString(status.Profile) {
		return SessionAuthority{}, fmt.Errorf("Airelay session-status key or profile mismatch")
	}
	return status, nil
}

// ResolveSessionAuthority resolves one exact server-owned base session key.
// It never scans or infers from the active-session inventory.
func (c Client) ResolveSessionAuthority(ctx context.Context, key string, requireUsable bool) (SessionAuthority, error) {
	status, err := c.executionStatus(ctx, key)
	if err != nil {
		return SessionAuthority{}, err
	}
	if !status.ControllerReachable || status.State == "error" || requireUsable && status.State != "idle" {
		return SessionAuthority{}, fmt.Errorf("Airelay session %q is not usable", key)
	}
	return status, nil
}

func normalizeCWD(value string) string {
	if value == "" {
		return ""
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	resolved, err := filepath.Abs(value)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

func (c Client) runJSON(ctx context.Context, args []string, target any) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Command, args...)
	cmd.Env = cleanEnv()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 1<<20, 8192
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if ctx.Err() != nil || stdout.exceeded || stderr.exceeded {
		return fmt.Errorf("bounded Airelay query failed")
	}
	return json.Unmarshal([]byte(stdout.String()), target)
}
