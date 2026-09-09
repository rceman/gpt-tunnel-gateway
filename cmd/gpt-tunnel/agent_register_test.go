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

func TestAgentRegisterCLISyntaxRejectsMalformedArguments(t *testing.T) {
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
	airelay := filepath.Join(dir, "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := json.Marshal(config.Config{
		SchemaVersion: 1, GatewayID: "test-gateway", ListenAddr: "127.0.0.1:8875",
		StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20,
		MaxListItems: 1000, DispatchTimeoutSeconds: 1, RunTimeoutSeconds: 60,
		AirelayCommand: airelay,
		Hub:            config.HubConfig{RepositoryURL: dir, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"},
		Controller:     config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8876"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"agent", "register", "example"},
		{"agent", "register", "example", "coder", "--project-code", "MCP"},
		{"agent", "register", "example", "--expected-hub-revision", "coder"},
		{"agent", "register", "example", "coder", "--expected-hub-revision"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "usage:") {
			t.Fatalf("arguments %#v were not rejected as usage: err=%v output=%s", args, err, output)
		}
	}
}
