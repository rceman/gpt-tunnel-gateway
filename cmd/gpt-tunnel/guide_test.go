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
		"Zero-state: gpt-tunnel guide",
		"project-bound agent/guide and task/guide",
		"Never scan Gateway Hub/SQLite or unrelated home directories",
		"active durable Session fixes the role",
		"Planner owns durable WHAT/WHY",
		"Planner is not the dispatcher, Worker supervisor, technical reviewer, tester, or integration proxy",
		"Lead owns dispatch, Worker supervision, technical review/rework, verification, integration, continuation, Track submission",
		"Worker implements assigned Tasks and makes one production+tests candidate handoff through gpt-tunnel task submit-code",
		"Planner delegates one Track through durable MSG carrying only its key",
		"reuses persistent execution after restart",
		"focused/affected deterministic tests plus scripts/test-fast.py",
		"Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E",
		"Gateway action schemas and accepted project Rules govern workflow",
		"ADR72 Gates 1-20 are the sole review taxonomy",
		"journal/contract is the sole journal stream-rules authority",
		"Final project activation/release waits for source-bound Planner Track review",
	} {
		if !strings.Contains(string(output), text) {
			t.Fatalf("zero-state guide omitted %q", text)
		}
	}
	for _, stale := range []string{"queue", "train", "hotfix", "wave", "submit-tests", "runtime-ref", "submit-rebase"} {
		if strings.Contains(strings.ToLower(string(output)), stale) {
			t.Fatalf("zero-state guide retains stale workflow guidance %q", stale)
		}
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

func TestGuideIsAvailableWithoutSearchUtilities(t *testing.T) {
	bin := buildGuideCLI(t)
	cmd := exec.Command(bin, "guide")
	cmd.Env = []string{"PATH=" + t.TempDir(), "GPT_TUNNEL_CONFIG=/definitely/missing.json"}
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("zero-state guide failed without search utilities: %v", err)
	}
	if !strings.Contains(string(output), "gpt-tunnel guide") {
		t.Fatalf("zero-state guide output=%s", output)
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
