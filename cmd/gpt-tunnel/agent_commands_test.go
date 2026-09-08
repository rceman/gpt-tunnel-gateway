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

func TestAgentTailCLIFailsClosedWithoutLocalStoreOrAirelay(t *testing.T) {
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
	dir := t.TempDir()
	marker := filepath.Join(dir, "airelay-called")
	command := filepath.Join(dir, "airelay")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := json.Marshal(config.Config{
		SchemaVersion: 1, GatewayID: "test-gateway", ListenAddr: "127.0.0.1:8875",
		StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20,
		MaxListItems: 1000, DispatchTimeoutSeconds: 1, RunTimeoutSeconds: 60,
		AirelayCommand: command, Hub: config.HubConfig{RepositoryURL: dir, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"},
		Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8876"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "agent", "tail", "example", "--session", "SA-GTW-BEYB", "--lines", "1")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CLI tail unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "CLI transport is deferred to GTW-TSK547") {
		t.Fatalf("CLI tail did not fail with bounded deferred message: %s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("CLI invoked Airelay: stat=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatalf("CLI created/used local state: stat=%v", err)
	}
}
