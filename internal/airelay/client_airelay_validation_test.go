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

func writeValidationFixture(t *testing.T, active bool) (Client, string, string) {
	t.Helper()
	dir := t.TempDir()
	command := filepath.Join(dir, "airelay")
	marker := filepath.Join(dir, "launch-marker")
	worktree := filepath.Join(dir, "worktree")
	if err := os.Mkdir(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	activeJSON := "[]"
	historyJSON := "[]"
	if active {
		activeJSON = fmt.Sprintf(`[{"sessionKey":"gtw_lane_", "profile":"coding", "cwd":%q}]`, worktree)
		historyJSON = fmt.Sprintf(`[{"sessionKey":"gtw_lane_", "profile":"coding", "invocationCwd":%q}]`, worktree)
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
sessions) printf '%%s' '%s' ;;
history) printf '%%s' '%s' ;;
session-status) printf '{"sessionKey":"%%s","profile":"coding","controllerReachable":true,"state":"idle"}' "$2" ;;
start|start-session) printf 'launched' > '%s'; exit 99 ;;
*) exit 99 ;;
esac
`, activeJSON, historyJSON, marker)
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return Client{Command: command, Timeout: time.Second}, marker, worktree
}

func TestDeriveExecutionSessionKeyRemainsPureAndDeterministic(t *testing.T) {
	first, err := DeriveExecutionSessionKey("base_session", "coding", "GTW-TRN64")
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeriveExecutionSessionKey("base_session", "coding", "GTW-TRN64")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.HasPrefix(first, "gtw_lane_") {
		t.Fatalf("derived key=%q,%q", first, second)
	}
	if _, err := DeriveExecutionSessionKey("", "coding", "GTW-TRN64"); err == nil {
		t.Fatal("empty base session was accepted")
	}
}

func TestValidateExecutionSessionAcceptsExactExistingLaneWithoutLaunch(t *testing.T) {
	client, marker, worktree := writeValidationFixture(t, true)
	key, err := DeriveExecutionSessionKey("base_session", "coding", "GTW-TRN64")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's records use the exact derived key; replace the placeholder
	// in the command script without introducing a launch-capable path.
	data, err := os.ReadFile(client.Command)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client.Command, []byte(strings.ReplaceAll(string(data), "gtw_lane_", key)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateExecutionSession(context.Background(), ExecutionSessionRequest{
		BaseSessionKey: "base_session", Profile: "coding", WorktreePath: worktree, Identity: "GTW-TRN64",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("validation invoked launch path: %v", err)
	}
}

func TestValidateExecutionSessionMissingLaneFailsClosedWithoutLaunch(t *testing.T) {
	client, marker, worktree := writeValidationFixture(t, false)
	if err := client.ValidateExecutionSession(context.Background(), ExecutionSessionRequest{
		BaseSessionKey: "base_session", Profile: "coding", WorktreePath: worktree, Identity: "GTW-TRN64",
	}); err == nil {
		t.Fatal("missing legacy execution lane was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("failed validation invoked launch path: %v", err)
	}
}
