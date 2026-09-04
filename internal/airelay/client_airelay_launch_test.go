package airelay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func dynamicExecutionSessionTestClient(t *testing.T, statusMode string) (Client, string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	active := filepath.Join(dir, "active.json")
	history := filepath.Join(dir, "history.json")
	starts := filepath.Join(dir, "starts")
	body := fmt.Sprintf("#!/bin/sh\nactive='%s'\nhistory='%s'\nstarts='%s'\ncase \"$1\" in\nsessions) if [ -f \"$active\" ]; then cat \"$active\"; else printf '[]'; fi ;;\nhistory) if [ -f \"$history\" ]; then cat \"$history\"; else printf '[]'; fi ;;\nsession-status) if [ \"%s\" = key ]; then printf '{\"sessionKey\":\"wrong\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}'; elif [ \"%s\" = profile ]; then printf '{\"sessionKey\":\"%%s\",\"profile\":\"other\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"; else printf '{\"sessionKey\":\"%%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"; fi ;;\nstart) printf 'start\\n' >> \"$starts\"; printf '[{\"sessionKey\":\"__KEY__\",\"profile\":\"%%s\",\"cwd\":\"__WORKTREE__\"}]' \"$2\" > \"$active\"; printf '[{\"sessionKey\":\"__KEY__\",\"profile\":\"%%s\",\"invocationCwd\":\"__WORKTREE__\"}]' \"$2\" > \"$history\" ;;\nesac\n", active, history, starts, statusMode, statusMode)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return Client{Command: script, Timeout: time.Second}, active, starts
}

func TestEnsureExecutionSessionFirstLaunchAndSafeRelaunch(t *testing.T) {
	client, active, starts := dynamicExecutionSessionTestClient(t, "ok")
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(string(contents), "__KEY__", key))
	contents = []byte(strings.ReplaceAll(string(contents), "__WORKTREE__", worktree))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := client.EnsureExecutionSession(context.Background(), in); err != nil || got != key {
		t.Fatalf("first launch=%q, %v", got, err)
	}
	if err := os.Remove(active); err != nil {
		t.Fatal(err)
	}
	if got, err := client.EnsureExecutionSession(context.Background(), in); err != nil || got != key {
		t.Fatalf("safe relaunch=%q, %v", got, err)
	}
	data, err := os.ReadFile(starts)
	if err != nil || strings.Count(string(data), "start") != 2 {
		t.Fatalf("launch count=%q err=%v", data, err)
	}
}

func TestEnsureExecutionSessionBoundsDetachedLaunch(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1\" = start ]; then sleep 2; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := Client{Command: script, Timeout: 20 * time.Millisecond}
	in, _, _ := executionSessionTestInput(t, client)
	if _, err := client.EnsureExecutionSession(context.Background(), in); err == nil {
		t.Fatal("detached launch exceeded timeout without failing")
	}
}

func TestEnsureExecutionSessionRejectsStatusIdentityConflict(t *testing.T) {
	for _, mode := range []string{"key", "profile"} {
		client, active, _ := dynamicExecutionSessionTestClient(t, mode)
		in, key, worktree := executionSessionTestInput(t, client)
		contents, err := os.ReadFile(client.Command)
		if err != nil {
			t.Fatal(err)
		}
		contents = []byte(strings.ReplaceAll(strings.ReplaceAll(string(contents), "__KEY__", key), "__WORKTREE__", worktree))
		if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(active, []byte(fmt.Sprintf("[{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"cwd\":\"%s\"}]", key, worktree)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := client.EnsureExecutionSession(context.Background(), in); err == nil {
			t.Fatalf("status %s conflict was accepted", mode)
		}
	}
}

func TestEnsureExecutionSessionConcurrentCallsLaunchOnce(t *testing.T) {
	client, _, starts := dynamicExecutionSessionTestClient(t, "ok")
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(strings.ReplaceAll(string(contents), "__KEY__", key), "__WORKTREE__", worktree))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			got, callErr := client.EnsureExecutionSession(context.Background(), in)
			if callErr == nil && got != key {
				callErr = fmt.Errorf("key=%q", got)
			}
			errs <- callErr
		}()
	}
	group.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent EnsureExecutionSession successes=%d, want exactly one", successes)
	}
	data, err := os.ReadFile(starts)
	if err != nil || strings.Count(string(data), "start") != 1 {
		t.Fatalf("concurrent launch count=%q err=%v", data, err)
	}
}

func executionSessionTestInput(t *testing.T, client Client) (ExecutionSessionRequest, string, string) {
	t.Helper()
	worktree := filepath.Join(t.TempDir(), "lane")
	lockDir := filepath.Join(t.TempDir(), "locks")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
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

func TestEnsureExecutionSessionRejectsConflictingHistory(t *testing.T) {
	client := executionSessionTestClient(t, `[ {"sessionKey":"KEY","profile":"coding","cwd":"WORKTREE"} ]`, `[ {"sessionKey":"KEY","profile":"other","invocationCwd":"WORKTREE"} ]`, `{"sessionKey":"KEY","profile":"coding","controllerReachable":true,"state":"idle"}`)
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(strings.ReplaceAll(string(contents), "KEY", key), "WORKTREE", worktree))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsureExecutionSession(context.Background(), in); err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatalf("conflicting history error=%v", err)
	}
}

func TestValidateExecutionSessionRejectsRecordedSessionMismatchWithoutLaunch(t *testing.T) {
	client := executionSessionTestClient(t, `[ {"sessionKey":"KEY","profile":"coding","cwd":"WRONG"} ]`, `[ {"sessionKey":"KEY","profile":"coding","invocationCwd":"WORKTREE"} ]`, `{"sessionKey":"KEY","profile":"coding","controllerReachable":true,"state":"idle"}`)
	in, key, worktree := executionSessionTestInput(t, client)
	contents, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(strings.ReplaceAll(string(contents), "KEY", key), "WORKTREE", worktree))
	contents = []byte(strings.ReplaceAll(string(contents), "WRONG", filepath.Join(t.TempDir(), "wrong-lane")))
	if err := os.WriteFile(client.Command, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateExecutionSession(context.Background(), in); err == nil || !strings.Contains(err.Error(), "mismatched authority") {
		t.Fatalf("recorded session mismatch error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(client.Command), "starts")); !os.IsNotExist(err) {
		t.Fatalf("validate-only mismatch launched a session: %v", err)
	}
}
