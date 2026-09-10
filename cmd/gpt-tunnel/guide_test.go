package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/agentguide"
)

func TestGuideIsZeroStateAndMatchesCanonicalContent(t *testing.T) {
	bin := buildGuideCLI(t)
	cmd := exec.Command(bin, "guide")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+filepath.Join(t.TempDir(), "missing.json"))
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("zero-state guide failed: %v", err)
	}
	var got agentguide.Content
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("guide output is not JSON: %v\n%s", err, output)
	}
	if !reflect.DeepEqual(got, agentguide.Canonical()) {
		t.Fatalf("guide content differs from canonical source: got=%#v want=%#v", got, agentguide.Canonical())
	}
	if !strings.Contains(got.CLIUsage, "before work, use gpt-tunnel task read <key>") {
		t.Fatalf("guide CLI usage omitted exact Task read command: %q", got.CLIUsage)
	}
	for _, text := range []string{
		"Planner owns Task/ADR decisions",
		"git branch --show-current",
		"Never scan ~/.local/share/gpt-tunnel-gateway",
		"roughly 2-3 commands",
		"immutable checkpoint",
		"deterministic fakes or mocks",
		"gpt-tunnel project list",
		"The CLI has no Task list surface",
		"task work/finalize are execution mutations",
		"GOOD reposuite README-only proof",
		"BAD: git log --all",
		"Prefer rg for source search",
		"repo-local grep, find, or sed",
		"Agent-native bounded read/search tools are also valid",
	} {
		if !strings.Contains(string(output), text) {
			t.Fatalf("guide omitted critical text %q", text)
		}
	}
	lower := strings.ToLower(string(output))
	if strings.Contains(string(output), "rg --files") || strings.Contains(lower, "fallback") || strings.Contains(lower, "install rg") || strings.Contains(lower, "sudo ") || strings.Contains(lower, "package manager") {
		t.Fatalf("guide contains an unavailable or prohibited instruction: %s", output)
	}
}

func TestGuideRejectsArgumentsBeforeLoadingConfig(t *testing.T) {
	bin := buildGuideCLI(t)
	cmd := exec.Command(bin, "guide", "unexpected")
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+filepath.Join(t.TempDir(), "missing.json"))
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "usage: gpt-tunnel") {
		t.Fatalf("malformed guide arguments were not rejected as usage: err=%v output=%s", err, output)
	}
}

func TestGuideDocumentsBoundedAlternativeWhenRGIsUnavailable(t *testing.T) {
	bin := buildGuideCLI(t)
	cmd := exec.Command(bin, "guide")
	cmd.Env = []string{"PATH=" + t.TempDir(), "GPT_TUNNEL_CONFIG=/definitely/missing.json"}
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("guide failed without rg on PATH: %v", err)
	}
	text := string(output)
	for _, required := range []string{
		"Prefer rg for source search",
		"if it is absent",
		"repo-local grep, find, or sed",
		"tool absence never broadens scope",
		"assigned repository/worktree",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("no-rg guide omitted %q: %s", required, text)
		}
	}
	lower := strings.ToLower(text)
	if strings.Contains(text, "rg --files") || strings.Contains(lower, "fallback") || strings.Contains(lower, "install rg") || strings.Contains(lower, "sudo ") || strings.Contains(lower, "package manager") {
		t.Fatalf("no-rg guide contains an unavailable or prohibited instruction: %s", text)
	}
}

func buildGuideCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gpt-tunnel")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return bin
}
