package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestGitcmdResolvesManagedProjectAfterServiceConstruction(t *testing.T) {
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	s := service.New(config.Config{StateDir: stateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000})
	current := config.EmptyManagedProjectRegistry()
	digest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	next := config.ManagedProjectRegistry{SchemaVersion: config.ManagedProjectRegistrySchemaVersion, Revision: 1, Projects: map[string]config.ManagedProjectEntry{"managed": {Root: projectRoot, RepositoryURL: "git@example.invalid:managed.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "managed_master"}}}
	if _, err := config.WriteManagedProjectRegistry(stateDir, digest, next); err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	gitcmd(context.Background(), s, []string{"worktree-status", "managed"})
	_ = w.Close()
	os.Stdout = oldStdout
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"branch"`) || !strings.Contains(string(output), `"clean"`) {
		t.Fatalf("managed CLI Git route returned unexpected output: %s", output)
	}
}

func TestGitcmdFailsClosedForMalformedManagedRegistry(t *testing.T) {
	if os.Getenv("GPT_TUNNEL_MALFORMED_REGISTRY_CHILD") == "1" {
		stateDir := os.Getenv("GPT_TUNNEL_MALFORMED_REGISTRY_STATE")
		s := service.New(config.Config{StateDir: stateDir})
		gitcmd(context.Background(), s, []string{"worktree-status", "managed"})
		return
	}
	stateDir := t.TempDir()
	if err := os.WriteFile(config.ManagedProjectRegistryPath(stateDir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestGitcmdFailsClosedForMalformedManagedRegistry$")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_MALFORMED_REGISTRY_CHILD=1", "GPT_TUNNEL_MALFORMED_REGISTRY_STATE="+stateDir)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("malformed registry CLI route unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "managed project registry") {
		t.Fatalf("malformed registry CLI error was not surfaced: %s", output)
	}
}
