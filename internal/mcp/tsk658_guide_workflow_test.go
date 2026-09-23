package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTSK658TaskGuideSingleSubmitWorkflow(t *testing.T) {
	server := newSessionTestServer(t)
	value, err := tsk585ExecuteGeneric(t, server, "task/guide", map[string]any{"project_id": "example"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	assertTSK658SingleSubmitWorkflowGuide(t, string(encoded))

	server.genericActionMu.RLock()
	submitCode := server.genericActions["task/submit-code"]
	submitRebase := server.genericActions["task/submit-rebase"]
	server.genericActionMu.RUnlock()
	for _, action := range []GenericAction{submitCode, submitRebase} {
		if !strings.Contains(action.Description, "production and test candidate") || !strings.Contains(action.Description, "Lead review") {
			t.Fatalf("submit action description is stale: %#v", action)
		}
	}
}

func assertTSK658SingleSubmitWorkflowGuide(t *testing.T, text string) {
	t.Helper()
	for _, required := range []string{
		"Planner owns durable WHAT/WHY:",
		"ADR/Task/Rule and Milestone/Track composition",
		"Planner is not the dispatch",
		"Lead owns ordinary Task dispatch, Worker supervision, technical review/rework, verification, integration, continuation, Track submission",
		"Worker implements the assigned Task",
		"one production+tests submit-code handoff",
		"focused/affected deterministic tests plus scripts/test-fast.py",
		"Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E",
		"Lead performs project-required full Task verification after submission",
		"Server derives Track readiness",
		"track/submit",
		"Planner track/accept",
		"Lead never mutates Planner-owned semantics",
		"journal/contract is the sole stream-rules authority",
		"ADR72 Gates 1-20 are the sole review taxonomy",
		"Final project activation/release waits for source-bound Planner Track review",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("task/guide omitted PLAW workflow invariant %q", required)
		}
	}
	for _, stale := range []string{"queue", "train", "hotfix", "wave", "submit-tests", "runtime-ref", "submit-rebase", "tests-only checkpoint", "separate tests", "mandatory tests"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(stale)) {
			t.Fatalf("task/guide retains stale workflow guidance %q", stale)
		}
	}
}
