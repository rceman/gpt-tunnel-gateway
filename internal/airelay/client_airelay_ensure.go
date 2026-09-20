package airelay

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type DetachedEnsureResult struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Started bool   `json:"started,omitempty"`
}

type detachedRuntimeEntry struct {
	SessionKey          string `json:"sessionKey"`
	RuntimeID           string `json:"runtimeId"`
	Profile             string `json:"profile"`
	ControllerReachable bool   `json:"controllerReachable"`
}

func (c Client) EnsureDetached(ctx context.Context, profile, key string) (DetachedEnsureResult, error) {
	profile = strings.TrimSpace(profile)
	if !profileRE.MatchString(profile) || !sessionRE.MatchString(key) {
		return DetachedEnsureResult{}, fmt.Errorf("invalid detached Worker authority")
	}
	detached, found, err := c.resolveDetachedRuntime(ctx, key)
	if err != nil {
		return DetachedEnsureResult{}, err
	}
	if found {
		if detached.Profile != profile {
			return DetachedEnsureResult{}, fmt.Errorf("Airelay Worker key is bound to a conflicting profile")
		}
		if !detached.ControllerReachable {
			return DetachedEnsureResult{}, fmt.Errorf("Airelay Worker controller is unreachable")
		}
	}
	status, statusErr := c.executionStatus(ctx, key)
	if statusErr == nil {
		if status.Profile != profile {
			return DetachedEnsureResult{}, fmt.Errorf("Airelay Worker key is bound to a conflicting profile")
		}
		state := normalizeSessionState(status.State)
		if state != "error" {
			if !status.ControllerReachable {
				return DetachedEnsureResult{}, fmt.Errorf("Airelay Worker controller is unreachable")
			}
			return DetachedEnsureResult{
				Key:    key,
				Status: "reused",
			}, nil
		}
	} else if found {
		return DetachedEnsureResult{}, fmt.Errorf("existing Airelay Worker is not inspectable")
	}
	if err := c.checkLaunchHistoryProfile(ctx, profile, key); err != nil {
		return DetachedEnsureResult{}, err
	}
	if err := c.startDetached(ctx, profile, key); err != nil {
		return DetachedEnsureResult{}, err
	}
	if err := c.verifyStartedDetached(ctx, profile, key); err != nil {
		return DetachedEnsureResult{}, c.rollbackStartedWorker(key, err)
	}
	return DetachedEnsureResult{
		Key:     key,
		Status:  "started",
		Started: true,
	}, nil
}

func (c Client) resolveDetachedRuntime(ctx context.Context, key string) (detachedRuntimeEntry, bool, error) {
	var entries []detachedRuntimeEntry
	if err := c.runJSON(ctx, []string{"detached", "--json"}, &entries); err != nil {
		return detachedRuntimeEntry{}, false, fmt.Errorf("cannot inspect Airelay detached Workers")
	}
	var match detachedRuntimeEntry
	found := false
	for _, entry := range entries {
		if entry.SessionKey != key {
			continue
		}
		if found {
			return detachedRuntimeEntry{}, false, fmt.Errorf("Airelay detached Worker inventory is ambiguous")
		}
		match, found = entry, true
	}
	return match, found, nil
}

func (c Client) checkLaunchHistoryProfile(ctx context.Context, profile, key string) error {
	history, err := c.executionHistory(ctx)
	if err != nil {
		return fmt.Errorf("cannot inspect Airelay Worker launch history")
	}
	for _, entry := range history {
		if entry.SessionKey == key && entry.Profile != profile {
			return fmt.Errorf("Airelay Worker key has conflicting launch history")
		}
	}
	return nil
}

func (c Client) verifyStartedDetached(ctx context.Context, profile, key string) error {
	entry, found, err := c.resolveDetachedRuntime(ctx, key)
	if err != nil {
		return err
	}
	if !found || entry.Profile != profile || !entry.ControllerReachable {
		return fmt.Errorf("started Worker is not a healthy detached runtime")
	}
	status, err := c.ResolveSessionAuthority(ctx, key, true)
	if err != nil || status.Profile != profile {
		return fmt.Errorf("started Worker is not ready")
	}
	return nil
}

func (c Client) rollbackStartedWorker(key string, cause error) error {
	if err := c.StopDetached(context.Background(), key); err != nil {
		return fmt.Errorf("%w; detached Worker rollback failed", cause)
	}
	return cause
}

func (c Client) StopDetached(ctx context.Context, key string) error {
	if !sessionRE.MatchString(key) {
		return fmt.Errorf("invalid detached Worker authority")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Command, "stop", key)
	cmd.Env = cleanEnv()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 8192, 8192
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Airelay detached Worker stop failed")
	}
	if ctx.Err() != nil || stdout.exceeded || stderr.exceeded {
		return fmt.Errorf("Airelay detached Worker stop exceeded its bound")
	}
	return nil
}

func (c Client) startDetached(ctx context.Context, profile, key string) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Command, "start", profile, "--key", key, "--detached", "--bypass")
	cmd.Env = cleanEnv()
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 8192, 8192
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Airelay detached Worker start failed")
	}
	if ctx.Err() != nil || stdout.exceeded || stderr.exceeded {
		return fmt.Errorf("Airelay detached Worker start exceeded its bound")
	}
	return nil
}
