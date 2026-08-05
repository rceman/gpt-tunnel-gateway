package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func writeMCPManagedRegistry(t *testing.T, s *service.Service, root, projectID, repositoryURL string) config.ManagedProjectRegistry {
	t.Helper()
	current, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		t.Fatalf("load managed registry: %v", err)
	}
	digest, err := current.Digest()
	if err != nil {
		t.Fatalf("digest managed registry: %v", err)
	}
	next := config.ManagedProjectRegistry{SchemaVersion: config.ManagedProjectRegistrySchemaVersion, Revision: current.Revision + 1, Projects: map[string]config.ManagedProjectEntry{projectID: {Root: root, RepositoryURL: repositoryURL, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: projectID + "_master"}}}
	if _, err := config.WriteManagedProjectRegistry(s.Config.StateDir, digest, next); err != nil {
		t.Fatalf("write managed registry: %v", err)
	}
	return next
}

func executeMCPTool(t *testing.T, server *Server, name string, arguments map[string]any) (any, error) {
	t.Helper()
	tool, ok := server.tools()[name]
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), raw)
}

func TestManagedProjectResolutionMCPCapabilitiesAndGitAreDynamic(t *testing.T) {
	_, rootOne, _ := testutil.RepoWithBareRemote(t)
	_, rootTwo, _ := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	c := config.Config{GatewayID: "home_pc", ListenAddr: "127.0.0.1:8875", StateDir: stateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: "git@example.invalid:hub.git", Branch: "main"}}
	server := &Server{Service: service.New(c)}
	registry := writeMCPManagedRegistry(t, server.Service, rootOne, "managed", "git@example.invalid:managed.git")

	capabilities, err := executeMCPTool(t, server, "gateway_capabilities", map[string]any{})
	if err != nil {
		t.Fatalf("managed capabilities failed: %v", err)
	}
	capabilityJSON, _ := json.Marshal(capabilities)
	if !strings.Contains(string(capabilityJSON), "managed") || strings.Contains(string(capabilityJSON), rootOne) || strings.Contains(string(capabilityJSON), "managed_master") || strings.Contains(string(capabilityJSON), config.ManagedProjectMirrorPath(stateDir, "managed")) {
		t.Fatalf("capabilities leaked or omitted managed project: %s", capabilityJSON)
	}

	if err := os.WriteFile(filepath.Join(rootOne, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstStatus, err := executeMCPTool(t, server, "git_worktree_status", map[string]any{"project_id": "managed"})
	if err != nil {
		t.Fatalf("managed git status failed: %v", err)
	}
	firstJSON, _ := json.Marshal(firstStatus)
	if !strings.Contains(string(firstJSON), `"clean":false`) {
		t.Fatalf("first managed root was not used: %s", firstJSON)
	}

	current, err := config.LoadManagedProjects(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}
	replacement := config.ManagedProjectRegistry{SchemaVersion: config.ManagedProjectRegistrySchemaVersion, Revision: current.Revision + 1, Projects: map[string]config.ManagedProjectEntry{"managed": {Root: rootTwo, RepositoryURL: "git@example.invalid:managed.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "managed_master"}}}
	if _, err := config.WriteManagedProjectRegistry(stateDir, digest, replacement); err != nil {
		t.Fatal(err)
	}
	secondStatus, err := executeMCPTool(t, server, "git_worktree_status", map[string]any{"project_id": "managed"})
	if err != nil {
		t.Fatalf("replaced managed git status failed: %v", err)
	}
	secondJSON, _ := json.Marshal(secondStatus)
	if !strings.Contains(string(secondJSON), `"clean":true`) {
		t.Fatalf("replacement root was not used: %s", secondJSON)
	}
	if registry.Revision == replacement.Revision {
		t.Fatalf("replacement did not advance registry revision")
	}
}

func TestManagedProjectResolutionMCPFailsClosedAndDoesNotWriteAbsentRegistry(t *testing.T) {
	stateDir := t.TempDir()
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc", StateDir: stateDir})}
	if _, err := executeMCPTool(t, server, "gateway_capabilities", map[string]any{}); err != nil {
		t.Fatalf("static-only capabilities failed without registry: %v", err)
	}
	if _, err := os.Stat(config.ManagedProjectRegistryPath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("capabilities created registry, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "locks")); !os.IsNotExist(err) {
		t.Fatalf("capabilities created registry lock directory, stat error=%v", err)
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ManagedProjectRegistryPath(stateDir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeMCPTool(t, server, "gateway_capabilities", map[string]any{}); err == nil {
		t.Fatal("malformed registry fell back to static capabilities")
	}
	if _, err := executeMCPTool(t, server, "git_worktree_status", map[string]any{"project_id": "managed"}); err == nil {
		t.Fatal("malformed registry fell back to static Git resolution")
	}
	for _, name := range []string{
		"git_refresh", "git_refs", "git_log", "git_show", "git_tree", "git_read_file",
		"git_diff", "git_compare", "git_merge_base", "git_worktree_status", "git_worktree_diff",
	} {
		if _, err := executeMCPTool(t, server, name, map[string]any{"project_id": "managed"}); err == nil {
			t.Fatalf("malformed registry fell back for Git MCP tool %s", name)
		}
	}
}
