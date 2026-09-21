package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/agentguide"
	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK545AgentGuideIsClosedBoundedAndRoleAware(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "HOM", StateDir: t.TempDir()})}
	entry, ok := server.genericActionRegistry(server.tools())["agent/guide"]
	if !ok {
		t.Fatal("agent/guide is not registered")
	}
	if entry.AuthorityRole != durableSession.RolePlanner || !entry.LocalReadOnly || !entry.Annotations.ReadOnlyHint || !entry.Annotations.IdempotentHint {
		t.Fatalf("agent/guide metadata=%#v", entry)
	}
	if !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("agent/guide session binding=%#v", entry)
	}
	if entry.InputSchema["additionalProperties"] != false || len(schemaProperties(entry.InputSchema)) != 0 || len(stringList(entry.InputSchema["required"])) != 0 {
		t.Fatalf("agent/guide input schema=%#v", entry.InputSchema)
	}
	if entry.OutputSchema["additionalProperties"] != false {
		t.Fatalf("agent/guide output is not closed: %#v", entry.OutputSchema)
	}
	properties := schemaProperties(entry.OutputSchema)
	want := []string{"role_authority", "delegation", "startup", "canonical_state", "exploration_budget", "stop_fast", "checkpoints", "testing", "execution_example", "cli_usage", "architecture", "tail", "status_await", "prompt_interrupt", "authority"}
	if len(properties) != len(want) {
		t.Fatalf("agent/guide fields=%v", properties)
	}
	for _, field := range want {
		value, ok := properties[field].(map[string]any)
		if !ok || value["type"] != "string" || value["maxLength"] != 768 {
			t.Fatalf("agent/guide output field %q=%#v", field, properties[field])
		}
	}
	if got := stringList(entry.OutputSchema["required"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("agent/guide required=%v", got)
	}
	value, err := entry.Execute(authority.WithPlanner(context.Background()), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	guide, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("agent/guide result=%#v", value)
	}
	canonical := agentguide.Canonical()
	wantContent := map[string]string{
		"role_authority": canonical.RoleAuthority, "delegation": canonical.Delegation,
		"startup":         canonical.Startup,
		"canonical_state": canonical.CanonicalState, "exploration_budget": canonical.Exploration,
		"stop_fast": canonical.StopFast, "checkpoints": canonical.Checkpoints,
		"testing": canonical.Testing, "execution_example": canonical.ExecutionExample,
		"cli_usage": canonical.CLIUsage, "architecture": canonical.Architecture,
		"authority": canonical.Authority, "prompt_interrupt": canonical.PromptInterrupt,
		"status_await": canonical.StatusAwait, "tail": canonical.Tail,
	}
	for field, wantText := range wantContent {
		if gotText, ok := guide[field].(string); !ok || gotText != wantText {
			t.Fatalf("agent/guide field %q=%#v want=%q", field, guide[field], wantText)
		}
	}
	for _, text := range []string{
		"durable Planner, Lead, Advisor, and Worker",
		"Agent is a logical project identity",
		"Callers cannot select a durable Session",
		"logical Agent selector",
		"Train and watcher",
		"repo guide file",
		"Prefer rg for source search",
		"repo-local grep, find, or sed",
		"Agent-native bounded read/search tools are also valid",
	} {
		requireGuideConcept(t, guide, text)
	}
	for _, text := range []string{
		"planner alone", "planner-only", "under planner authority",
		"canonical wave", "agent/worker queue", "worker queue",
	} {
		forbidGuideConcept(t, guide, text)
	}
}

func guideText(guide map[string]any) string {
	parts := make([]string, 0, len(guide))
	for _, raw := range guide {
		if text, ok := raw.(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func requireGuideConcept(t *testing.T, guide map[string]any, concept string) {
	t.Helper()
	if !strings.Contains(guideText(guide), concept) {
		t.Fatalf("guide omitted %q: %#v", concept, guide)
	}
}

func forbidGuideConcept(t *testing.T, guide map[string]any, concept string) {
	t.Helper()
	if strings.Contains(strings.ToLower(guideText(guide)), strings.ToLower(concept)) {
		t.Fatalf("guide retains stale concept %q: %#v", concept, guide)
	}
}

func TestTSK545AgentGuideAllowsEveryAuthenticatedWorkflowSession(t *testing.T) {
	server := newSessionTestServer(t)
	store := mcpSQLiteSessionStore(t, server.Service)
	ref := "runtime-worker"
	session, err := store.Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleWorker,
		SessionType: durableSession.SessionTypeChatGPT,
		SessionRef:  &ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 545, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": session.ID, "action": "agent/guide", "input": map[string]any{},
		}},
	})))
	if response["is_error"] != false {
		t.Fatalf("authenticated Worker guide call was rejected: %#v", response)
	}
}

func TestTSK594GuidesStateCurrentRoleDelegationAndEvidenceAuthority(t *testing.T) {
	agent := canonicalAgentGuide()
	task := map[string]any{
		"workflow": taskGuideWorkflow, "review": taskGuideReview,
		"verification": taskGuideVerification, "completion": taskGuideCompletion,
		"boundaries": taskGuideBoundaries,
	}
	for _, concept := range []string{
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
		"Friction, lesson, and decision evidence goes through canonical journal/* actions; journal/contract is the sole stream-rules authority",
		"No direct Lead-to-Planner channel exists; owner/operator relay is only for semantic blockers and completed Track handoff, not execution proxy",
		"Final project activate/release waits for source-bound Planner Track review",
		"bounded ADR138 debug break-glass is only approved recovery",
		"Lead may run authorized non-final staging, disposable E2E, or preflight, including focused post-Task integration checks after risky Tasks, subsets, or Track end",
		"that is coding Agent/Worker lane discipline, not Lead integration",
		"Keep diagnostics bounded and retries explicit and bounded",
		"deterministic fakes or mocks",
	} {
		requireGuideConcept(t, agent, concept)
	}
	for _, concept := range []string{
		"Planner owns architecture, Task/Track scope, acceptance, dependencies/priority, and final Track semantic review, and is not the dispatch, supervision, review, test, or integration proxy",
		"Lead owns HOW",
		"Milestone Track is the Planner-to-Lead delegation unit, not an Agent/Worker/ad-hoc queue or a Wave",
		"Track order is planning intent, not FIFO",
		"Lead weighs membership, dependencies, priority, status, and Worker availability",
		"Any multiple Workers are assigned at dispatch",
		"Lead may run authorized non-final staging, disposable E2E, or preflight, including focused post-Task integration checks after risky Tasks, subsets, or Track end",
		"ADR138 role-permissive runtime does not transfer semantic authority between PLAW roles or make Worker-owned submit actions Lead-owned",
		"final project activate/release waits for source-bound Planner Track review",
		"Lead performs technical review and rework",
		"Worker submits one production+tests candidate through submit-code",
		"focused/affected checks plus scripts/test-fast.py only",
		"stops for Lead review",
		"After accepted code, plus any required rebase review, Lead owns task/test full verification",
		"journal/contract is the sole stream-rules authority",
		"there is no direct Lead-to-Planner channel",
		"owner/operator relay is only for semantic blockers or completed Track handoff, not execution proxy",
		"ADR72 Gates 1-20, including the Gates 9/12/14/19/20 public-response evidence requirements, are the sole gate taxonomy",
		"Lead owns lifecycle decisions but never hand-mutates Task lanes or canonical source via shell Git; canonical Task actions own the mechanics",
		"Lead never proxies a Worker submit or impersonates a Session",
		"Agents submit assigned-lane artifacts only through the fixed CLI gpt-tunnel task submit-code|submit-rebase, never native MCP",
		"pending Planner acceptance",
		"bootstrap_full proves the live assigned candidate from its frozen base and full gates",
	} {
		requireGuideConcept(t, task, concept)
	}
	for name, guide := range map[string]map[string]any{"agent": agent, "task": task} {
		for _, concept := range []string{
			"planner alone", "planner-only", "under planner authority",
			"planner review", "planner integration", "planner dispatches",
			"canonical wave", "agent/worker queue", "worker queue", "track is a wave",
		} {
			forbidGuideConcept(t, guide, concept)
		}
		for field, raw := range guide {
			text, ok := raw.(string)
			if !ok {
				continue
			}
			if n := utf8.RuneCountInString(text); n < 1 || n > 768 {
				t.Fatalf("%s guide %s runes=%d", name, field, n)
			}
			if strings.Contains(text, "Wave") && !strings.Contains(text, "not ") {
				t.Fatalf("%s guide %s claims a Wave without negation: %q", name, field, text)
			}
		}
	}
}
