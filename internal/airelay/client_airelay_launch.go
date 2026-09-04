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
)

// ExecutionSessionRequest identifies one server-owned execution lane. The
// caller supplies no user-controlled path; WorktreePath is resolved by GTW.
type ExecutionSessionRequest struct {
	BaseSessionKey string
	Profile        string
	WorktreePath   string
	Identity       string
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

type executionSessionStatus struct {
	SessionKey          string `json:"sessionKey"`
	Profile             string `json:"profile"`
	ControllerReachable bool   `json:"controllerReachable"`
	State               string `json:"state"`
}

// EnsureExecutionSession returns a deterministic lane-specific session key.
// Existing sessions are reused only after exact machine-readable authority
// checks. A mismatched session is never retargeted or restarted.
func (c Client) EnsureExecutionSession(ctx context.Context, in ExecutionSessionRequest) (string, error) {
	if !sessionRE.MatchString(in.BaseSessionKey) || in.WorktreePath == "" || filepath.Clean(in.WorktreePath) != in.WorktreePath || !filepath.IsAbs(in.WorktreePath) {
		return "", fmt.Errorf("invalid execution session authority")
	}
	if in.Identity == "" || strings.ContainsAny(in.Identity, "\x00\r\n") {
		return "", fmt.Errorf("invalid execution session identity")
	}
	worktree, err := filepath.Abs(in.WorktreePath)
	if err != nil {
		return "", fmt.Errorf("resolve execution worktree: %w", err)
	}
	sessions, err := c.executionSessions(ctx)
	if err != nil {
		return "", err
	}
	profile := strings.TrimSpace(in.Profile)
	for _, entry := range sessions {
		if entry.SessionKey == in.BaseSessionKey {
			if profile == "" {
				profile = entry.Profile
			} else if entry.Profile != profile {
				return "", fmt.Errorf("base Airelay session profile mismatch")
			}
		}
	}
	if profile == "" || !profileRE.MatchString(profile) {
		return "", fmt.Errorf("execution session profile is unavailable")
	}
	digest := sha256.Sum256([]byte(in.BaseSessionKey + "\x00" + profile + "\x00" + in.Identity))
	key := "gtw_lane_" + hex.EncodeToString(digest[:])[:32]

	matched := false
	for _, entry := range sessions {
		if entry.SessionKey != key {
			continue
		}
		if entry.Profile != profile || normalizeCWD(entry.CWD) != worktree {
			return "", fmt.Errorf("execution Airelay session %q has mismatched authority", key)
		}
		matched = true
	}
	history, err := c.executionHistory(ctx)
	if err != nil {
		return "", err
	}
	historyMatch := false
	for _, entry := range history {
		if entry.SessionKey != key {
			continue
		}
		if entry.Profile != profile || normalizeCWD(entry.InvocationCWD) != worktree {
			return "", fmt.Errorf("execution Airelay launch history %q has mismatched authority", key)
		}
		historyMatch = true
	}
	if matched != historyMatch {
		return "", fmt.Errorf("execution Airelay session %q has incomplete launch authority", key)
	}
	if !matched {
		cmd := exec.CommandContext(ctx, c.Command, "start", profile, "--key", key, "--detached")
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
		matched, historyMatch = false, false
		for _, entry := range sessions {
			if entry.SessionKey == key {
				if entry.Profile != profile || normalizeCWD(entry.CWD) != worktree {
					return "", fmt.Errorf("new execution Airelay session %q has mismatched authority", key)
				}
				matched = true
			}
		}
		for _, entry := range history {
			if entry.SessionKey == key {
				if entry.Profile != profile || normalizeCWD(entry.InvocationCWD) != worktree {
					return "", fmt.Errorf("new execution Airelay history %q has mismatched authority", key)
				}
				historyMatch = true
			}
		}
		if !matched || !historyMatch {
			return "", fmt.Errorf("execution Airelay launch %q was not durably recorded", key)
		}
	}
	status, err := c.executionStatus(ctx, key)
	if err != nil || !status.ControllerReachable || status.State == "error" {
		if err != nil {
			return "", fmt.Errorf("execution Airelay session is not reachable: %w", err)
		}
		return "", fmt.Errorf("execution Airelay session is not reachable")
	}
	return key, nil
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

func (c Client) executionStatus(ctx context.Context, key string) (executionSessionStatus, error) {
	var status executionSessionStatus
	if err := c.runJSON(ctx, []string{"session-status", key, "--json", "--no-warn"}, &status); err != nil {
		return executionSessionStatus{}, err
	}
	if status.SessionKey != "" && status.SessionKey != key {
		return executionSessionStatus{}, fmt.Errorf("Airelay session-status key mismatch")
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
