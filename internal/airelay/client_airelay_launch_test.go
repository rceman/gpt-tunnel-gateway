package airelay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func executionSessionTestClient(t *testing.T, sessions, history, status string) Client {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	log := filepath.Join(dir, "starts")
	body := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nsessions) printf '%%s' '%s' ;;\nhistory) printf '%%s' '%s' ;;\nsession-status) printf '%%s' '%s' ;;\nstart) printf 'start\\n' >> '%s' ;;\nesac\n", sessions, history, status, log)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return Client{Command: script, Timeout: time.Second}
}

func executionSessionTestInput(t *testing.T, client Client) (ExecutionSessionRequest, string, string) {
	t.Helper()
	worktree := filepath.Join(t.TempDir(), "lane")
	lockDir := filepath.Join(t.TempDir(), "locks")
	key, err := DeriveExecutionSessionKey("base-session", "coding", "train:example:GTW-TRN1")
	if err != nil {
		t.Fatal(err)
	}
	return ExecutionSessionRequest{
		BaseSessionKey: "base-session", Profile: "coding", WorktreePath: worktree,
		Identity: "train:example:GTW-TRN1", LockDir: lockDir,
	}, key, worktree
}

func TestEnsureExecutionSessionAllowsMultipleMatchingHistoryRecords(t *testing.T) {
	client := executionSessionTestClient(t, `[{"sessionKey":"KEY","profile":"coding","cwd":"WORKTREE"}]`, `[{"sessionKey":"KEY","profile":"coding","invocationCwd":"WORKTREE"},{"sessionKey":"KEY","profile":"coding","invocationCwd":"WORKTREE"}]`, `{"sessionKey":"KEY","profile":"coding","controllerReachable":true,"state":"idle"}`)
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(string(contents), "KEY", key))
	contents = []byte(strings.ReplaceAll(string(contents), "WORKTREE", worktree))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := client.EnsureExecutionSession(context.Background(), in)
	if err != nil || got != key {
		t.Fatalf("EnsureExecutionSession()=%q, %v; want %q", got, err, key)
	}
}

func TestEnsureExecutionSessionRejectsDuplicateActiveAuthority(t *testing.T) {
	client := executionSessionTestClient(t, `[{"sessionKey":"KEY","profile":"coding","cwd":"WORKTREE"},{"sessionKey":"KEY","profile":"coding","cwd":"WORKTREE"}]`, `[]`, `{}`)
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(strings.ReplaceAll(string(contents), "KEY", key), "WORKTREE", worktree))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsureExecutionSession(context.Background(), in); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate active authority error=%v", err)
	}
}

func TestValidateExecutionSessionNeverLaunchesInactiveLane(t *testing.T) {
	client := executionSessionTestClient(t, `[]`, `[{"sessionKey":"KEY","profile":"coding","invocationCwd":"WORKTREE"}]`, `{}`)
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(strings.ReplaceAll(string(contents), "KEY", key), "WORKTREE", worktree))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateExecutionSession(context.Background(), in); err == nil {
		t.Fatal("inactive lane unexpectedly validated")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(client.Command), "starts")); !os.IsNotExist(err) {
		t.Fatalf("validate-only path launched a session: %v", err)
	}
}

func TestEnsureExecutionSessionRequiresExactStatusProfile(t *testing.T) {
	client := executionSessionTestClient(t, `[{"sessionKey":"KEY","profile":"coding","cwd":"WORKTREE"}]`, `[{"sessionKey":"KEY","profile":"coding","invocationCwd":"WORKTREE"}]`, `{"sessionKey":"KEY","profile":"other","controllerReachable":true,"state":"idle"}`)
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(strings.ReplaceAll(string(contents), "KEY", key), "WORKTREE", worktree))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsureExecutionSession(context.Background(), in); err == nil {
		t.Fatal("status profile mismatch unexpectedly accepted")
	}
}
