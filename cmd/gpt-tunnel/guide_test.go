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
		"Planner owns WHAT/WHY, architecture, durable semantics, ADR/Task/RULE plus Milestone/Track composition and scope, acceptance, dependencies/priority, final Track semantic review, and executable-work curation",
		"it is not the dispatcher/supervisor/review/test/integrate proxy",
		"Lead owns HOW: dispatch, Worker supervision, technical review/rework, verification, integration, continuation and lifecycle decisions",
		"never mutates Planner semantics",
		"never creates or updates Planner-owned Tasks, Tracks, ADRs, or Rules",
		"never hand-mutates lanes or canonical source via shell Git; canonical Task actions own mechanics",
		"Worker owns implementation/testing and submits one production+tests candidate only via CLI gpt-tunnel task submit-code|submit-rebase, never native MCP",
		"Before submit-code, Worker runs only focused/affected deterministic tests plus cache-aware scripts/test-fast.py",
		"Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E",
		"Lead owns task/test full verification",
		"ADR138 role-permissive runtime does not transfer semantic authority between PLAW roles or make Worker-owned submit actions Lead-owned",
		"Lead never proxies a Worker submit or impersonates a Session",
		"Milestone Track is the Planner-to-Lead delegation unit, not an Agent/Worker/ad-hoc queue and not a Wave",
		"Track order is planning intent, not FIFO",
		"Lead weighs membership, dependencies, priority, status, and Worker availability",
		"Any multiple Workers are assigned at dispatch time",
		"a sidekick or advisor is advisory only and holds no lane or Task authority",
		"No task/queue/Agent queue, Planner/Worker impersonation, or alternate-role bypass",
		"ADR72 Gates 1-20 remain the sole gate taxonomy, including the Gates 9/12/14/19/20 public-response evidence requirements",
		"there is no parallel gate taxonomy",
		"journal/contract is the sole stream-rules authority",
		"No direct Lead-to-Planner channel exists",
		"owner/operator relay is only for semantic blockers and completed Track handoff, not execution proxy",
		"Final project activate/release waits for source-bound Planner Track review",
		"bounded ADR138 debug break-glass is only approved recovery",
		"Lead may run authorized non-final staging, disposable E2E, or preflight, including focused post-Task integration checks after risky Tasks, subsets, or Track end",
		"Keep diagnostics bounded and retries explicit and bounded",
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
	for _, stale := range []string{"Planner alone", "under Planner authority", "canonical wave", "Agent/Worker queue"} {
		if strings.Contains(string(output), stale) {
			t.Fatalf("guide retains stale concept %q: %s", stale, output)
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
