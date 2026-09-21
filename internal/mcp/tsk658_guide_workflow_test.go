package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK658TaskGuideSingleSubmitWorkflow(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "HOM", StateDir: t.TempDir()})}
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
		"submit-code",
		"submit-rebase",
		"production+tests",
		"focused/affected",
		"scripts/test-fast.py",
		"never go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E",
		"stops for Lead review",
		"Lead owns task/test full verification",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("task/guide omitted single-submit invariant %q", required)
		}
	}
	for _, stale := range []string{"submit-tests", "submit tests", "production-first", "production first", "tests-only checkpoint", "test artifact", "tests artifact", "separate test", "separate tests", "distinct test", "distinct tests", "mandatory tests"} {
		if strings.Contains(strings.ToLower(text), stale) {
			t.Fatalf("task/guide retains stale submit workflow %q", stale)
		}
	}
}
