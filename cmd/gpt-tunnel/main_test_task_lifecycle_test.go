package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestTaskDeferCLIRouteIsRetired(t *testing.T) {
	workdir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "gpt-tunnel")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = workdir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	stateDir := t.TempDir()
	c := config.Config{
		SchemaVersion:          1,
		GatewayID:              "test_gateway",
		ListenAddr:             "127.0.0.1:8875",
		StateDir:               stateDir,
		MaxReadBytes:           1,
		MaxDiffBytes:           1,
		MaxListItems:           1,
		DispatchTimeoutSeconds: 1,
		RunTimeoutSeconds:      60,
		AirelayCommand:         "airelay",
		Hub: config.HubConfig{
			RepositoryURL: stateDir,
			Branch:        "main",
			AuthorName:    "Gateway",
			AuthorEmail:   "gateway@example.invalid",
		},
		Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8766"},
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "task", "defer", "CODE-TSK1", "--reason", "retired")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("retired task defer route unexpectedly succeeded: %s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("task defer did not fail with CLI usage exit: err=%v output=%s", err, output)
	}
	if !strings.Contains(string(output), "usage: gpt-tunnel") || strings.Contains(string(output), `"status": "deferred"`) {
		t.Fatalf("task defer was not rejected by the current CLI contract: %s", output)
	}
}
