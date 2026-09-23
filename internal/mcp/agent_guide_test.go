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
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK545AgentGuideIsClosedBoundedAndRoleAware(t *testing.T) {
	server := newSessionTestServer(t)
	entry, ok := server.genericActionRegistry(server.tools())["agent/guide"]
	if !ok {
		t.Fatal("agent/guide is not registered")
	}
	if entry.AuthorityRole != actionRolePlannerOrLead || !entry.LocalReadOnly || !entry.Annotations.ReadOnlyHint || !entry.Annotations.IdempotentHint {
		t.Fatalf("agent/guide metadata=%#v", entry)
	}
	if !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("agent/guide session binding=%#v", entry)
	}
	if entry.InputSchema["additionalProperties"] != false || len(schemaProperties(entry.InputSchema)) != 0 || len(stringList(entry.InputSchema["required"])) != 0 {
		t.Fatalf("agent/guide input schema=%#v", entry.InputSchema)
	}
	variants, ok := entry.OutputSchema["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("agent/guide output variants=%#v", entry.OutputSchema)
	}
	wantFields := []string{"role_authority", "delegation", "startup", "canonical_state", "exploration_budget", "stop_fast", "checkpoints", "testing", "execution_example", "cli_usage", "architecture", "tail", "status_await", "prompt_interrupt", "authority"}
	for _, variant := range variants {
		schema, ok := variant.(map[string]any)
		if !ok || schema["additionalProperties"] != false {
			t.Fatalf("agent/guide variant is not closed: %#v", variant)
		}
		properties := schemaProperties(schema)
		source := properties["source"].(map[string]any)
		if source["const"] != "builtin" && source["const"] != "rule" {
			t.Fatalf("agent/guide source schema=%#v", source)
		}
		for _, field := range wantFields {
			value, ok := properties[field].(map[string]any)
			if !ok || value["type"] != "string" || value["maxLength"] != model.GuideTextMaxRunes {
				t.Fatalf("agent/guide output field %q=%#v", field, properties[field])
			}
		}
		if source["const"] == "rule" {
			ruleID, ok := properties["rule"].(map[string]any)
			if !ok || ruleID["minLength"] != 8 || ruleID["maxLength"] != model.MaxRuleIDLength || ruleID["pattern"] != model.RuleIDPattern {
				t.Fatalf("RUL projection identity schema=%#v", ruleID)
			}
			revision, ok := properties["rule_revision"].(map[string]any)
			if !ok || revision["type"] != "integer" || revision["minimum"] != 1 {
				t.Fatalf("RUL projection revision schema=%#v", revision)
			}
		}
	}
	value, err := entry.Execute(authority.WithPlanner(context.Background()), json.RawMessage(`{"project_id":"example"}`))
	if err != nil {
		t.Fatal(err)
	}
	guide, ok := value.(map[string]any)
	if !ok || guide["source"] != "builtin" {
		t.Fatalf("unbound agent/guide result=%#v", value)
	}
	canonical := agentguide.Canonical()
	wantContent := map[string]string{
		"role_authority": canonical.RoleAuthority, "delegation": canonical.Delegation,
		"startup": canonical.Startup, "canonical_state": canonical.CanonicalState,
		"exploration_budget": canonical.Exploration, "stop_fast": canonical.StopFast,
		"checkpoints": canonical.Checkpoints, "testing": canonical.Testing,
		"execution_example": canonical.ExecutionExample, "cli_usage": canonical.CLIUsage,
		"architecture": canonical.Architecture, "tail": canonical.Tail,
		"status_await": canonical.StatusAwait, "prompt_interrupt": canonical.PromptInterrupt,
		"authority": canonical.Authority,
	}
	for field, wantText := range wantContent {
		if gotText, ok := guide[field].(string); !ok || gotText != wantText {
			t.Fatalf("agent/guide field %q=%#v want=%q", field, guide[field], wantText)
		}
	}
	for _, stale := range []string{"queue", "train", "hotfix", "wave", "submit-tests", "runtime-ref", "submit-rebase"} {
		if strings.Contains(strings.ToLower(guideText(guide)), stale) {
			t.Fatalf("agent/guide retains stale guidance %q", stale)
		}
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
	agentText := guideText(agent)
	for _, concept := range []string{
		"Planner owns durable WHAT/WHY",
		"ADR/Task/Rule and Milestone/Track composition",
		"final Track review, and executable-work curation",
		"Planner is not the dispatcher, Worker supervisor, technical reviewer, tester, or integration proxy",
		"Lead owns dispatch, Worker supervision, technical review/rework, verification, integration, continuation, Track submission",
		"never mutates Planner-owned semantics",
		"Worker implements assigned Tasks and makes one production+tests candidate handoff through gpt-tunnel task submit-code",
		"Planner delegates one Track through durable MSG carrying only its key",
		"Ordered membership is planning intent, not FIFO",
		"reuses persistent execution after restart",
		"never dispatches while Worker has an actionable Task",
		"continues without ordinary Planner round-trips",
		"Never scan Gateway Hub/SQLite or unrelated home directories",
		"before work, use gpt-tunnel task read <key>",
		"Callers cannot select a durable Session",
		"Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E",
	} {
		if !strings.Contains(agentText, concept) {
			t.Fatalf("agent guide omitted current workflow concept %q: %s", concept, agentText)
		}
	}
	taskText := guideText(task)
	for _, concept := range []string{
		"Planner owns durable WHAT/WHY:",
		"ADR/Task/Rule and Milestone/Track composition",
		"Lead owns ordinary Task dispatch, Worker supervision, technical review/rework, verification, integration, continuation, Track submission",
		"Worker implements the assigned Task",
		"one production+tests submit-code handoff",
		"focused/affected deterministic tests plus scripts/test-fast.py",
		"Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E",
		"Lead performs project-required full Task verification after submission",
		"reuses persistent execution after restart",
		"never dispatches while Worker has an actionable Task",
		"continues without ordinary Planner round-trips",
		"Server derives Track readiness",
		"track/submit",
		"Planner track/accept",
		"genuine semantic, public-contract, security, persistence, or scope blockers",
		"Lead never mutates Planner-owned semantics, creates or updates Planner-owned Tasks, Tracks, ADRs, or Rules",
		"there is no ordinary Lead-to-Planner channel",
		"Final project activation/release waits for source-bound Planner Track review",
		"Keep diagnostics and retries bounded",
		"journal/contract is the sole stream-rules authority",
		"ADR72 Gates 1-20 are the sole review taxonomy",
	} {
		if !strings.Contains(taskText, concept) {
			t.Fatalf("task guide omitted current workflow concept %q: %s", concept, taskText)
		}
	}
	for name, guide := range map[string]map[string]any{"agent": agent, "task": task} {
		text := strings.ToLower(guideText(guide))
		for _, stale := range []string{"queue", "train", "hotfix", "wave", "submit-tests", "runtime-ref", "submit-rebase"} {
			if strings.Contains(text, stale) {
				t.Fatalf("%s guide retains stale guidance %q: %s", name, stale, text)
			}
		}
		for field, raw := range guide {
			text, ok := raw.(string)
			if !ok {
				continue
			}
			if n := utf8.RuneCountInString(text); n < 1 || n > model.GuideTextMaxRunes {
				t.Fatalf("%s guide %s runes=%d", name, field, n)
			}
		}
	}
	if !reflect.DeepEqual(model.GuideSubjects(), []string{"adr", "agent", "journal", "milestone", "rule", "task", "track"}) {
		t.Fatalf("guide subject order=%v", model.GuideSubjects())
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
