package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

func TestTSK599SchemaCostDoesNotExceedTSK598(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	counter := tokenizer.NewCounter()
	baseline := map[string]int{
		"task": 2765, "track": 1115, "operation": 148,
		"task/create": 446, "track/read": 1024, "operation/read": 396,
		"tools/list": 1888,
	}
	assertCost := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		tokens, err := counter.CountText(data)
		if err != nil {
			t.Fatal(err)
		}
		if tokens > baseline[name] {
			t.Errorf("%s schema cost=%d tokens, exceeds TSK598 baseline %d", name, tokens, baseline[name])
		}
		t.Logf("%s: tokens=%d bytes=%d baseline=%d", name, tokens, len(data), baseline[name])
	}
	for _, view := range []string{"task", "track", "operation", "task/create", "track/read", "operation/read"} {
		value, err := genericSchemaV2(entries, view)
		if err != nil {
			t.Fatal(err)
		}
		assertCost(view, value)
	}
	tools := server.publicTools()
	descriptors := make([]Tool, 0, len(canonicalToolManifest))
	for _, name := range canonicalToolManifest {
		descriptors = append(descriptors, tools[name])
	}
	assertCost("tools/list", descriptors)
}

func TestTSK599NoSupersededSchemaRegistriesOrDiscoveryBridges(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"toolOutputSchemas", "toolAnnotations", "mergeToolOutputSchemas", "coreToolOutputSchemas",
		"taskToolOutputSchemas", "runtimeToolOutputSchemas", "func (s *Server) genericSchema(",
		"genericActionCompactSummary", "genericActionSummary", "withProjectionDetail", "stripProjectionDetail",
		"compactCursorContractSchema", "compactCursorContractValue", "sessionStartPublicInputSchema", "sessionStartPublicOutputSchema",
		"func sessionRecordSchema(", "func sessionOutputSchema(", "taskLegacyRevision", "func taskRevisionOutputSchema(", "func taskCorrectionInputSchema(",
		"func runOutputSchema(", "func taskOutputSchema(", "func projectOutputSchema(", "func planOutputSchema(", "func adrOutputSchema(",
		"func operationReadOutputSchema(", "func completionGateResultOutputSchema(", "func workflowPolicyStatusOutputSchema(", "func projectOperationalStatusOutputSchema(",
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, symbol := range forbidden {
			if strings.Contains(string(contents), symbol) {
				t.Errorf("%s still contains superseded schema source %q", path, symbol)
			}
		}
	}
}

func TestTSK599EveryRegisteredActionIsContractProjectedAndHandled(t *testing.T) {
	server := newSessionTestServer(t)
	server.Service.Config.Debug.Enabled = true
	entries := server.genericActionRegistry(server.tools())
	contracts := server.actionContractSet()
	if len(entries) != len(contracts.Paths()) {
		t.Fatalf("registered actions=%d compiled contracts=%d", len(entries), len(contracts.Paths()))
	}
	for path, entry := range entries {
		contract, ok := contracts.Action(path)
		if !ok {
			t.Errorf("registered action %s has no compiled contract", path)
			continue
		}
		if entry.Execute == nil || entry.Contract.Path != path || !reflect.DeepEqual(entry.InputSchema, contract.Input.JSONSchema()) || !reflect.DeepEqual(entry.OutputSchema, contract.Output.JSONSchema()) {
			t.Errorf("registered action %s does not have one handler and one compiled contract projection", path)
		}
	}
}
