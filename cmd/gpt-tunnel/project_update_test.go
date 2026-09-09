package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestProjectUpdateCLIRequiresExactArgumentShape(t *testing.T) {
	hubBare, _, _ := testutil.RepoWithBareRemote(t)
	root := t.TempDir()
	c := config.Config{
		SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875",
		StateDir: filepath.Join(root, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20,
		MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60,
		AirelayCommand: "/bin/false", Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8876"},
		Hub:      config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Test", AuthorEmail: "test@example.invalid"},
		Projects: map[string]config.ProjectConfig{"example": {Root: root, Mirror: filepath.Join(root, "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "RSM", AirelaySessionKey: "example_master"}},
	}
	configPath := filepath.Join(root, "config.json")
	if err := fsutil.WriteJSONAtomic(configPath, c, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"project", "update", "example", "--project-code"},
		{"project", "update", "--project-code", "MCP", "example"},
		{"project", "update", "example", "MCP"},
	} {
		cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
		cmd.Dir = "."
		cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "usage: gpt-tunnel") {
			t.Fatalf("invalid project update args accepted without usage: args=%#v err=%v output=%s", args, err, output)
		}
	}
}
