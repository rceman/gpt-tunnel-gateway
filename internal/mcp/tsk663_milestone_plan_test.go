package mcp

import (
	"strings"
	"testing"
)

const tsk663MilestonePlanAction = "milestone/plan"

func TestTSK663MilestonePlanUsesCompiledActionContract(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries[tsk663MilestonePlanAction]
	if !ok {
		t.Fatal("milestone/plan handler is not registered")
	}
	contract, ok := server.actionContractSet().Action(tsk663MilestonePlanAction)
	if !ok {
		t.Fatal("milestone/plan contract is not compiled")
	}
	if contract.Metadata.Selector != "key" || !contract.Metadata.Annotations.ReadOnly || contract.Metadata.Annotations.Destructive {
		t.Fatalf("milestone/plan metadata=%#v", contract.Metadata)
	}
	if !entry.SessionBound || !entry.LocalReceiptOnly || !entry.Annotations.ReadOnlyHint || entry.AuthorityRole != actionRoleWorkflow {
		t.Fatalf("milestone/plan authority=%#v", entry.GenericAction)
	}
	input := schemaProperties(entry.InputSchema)
	if len(input) != 1 || input["key"] == nil {
		t.Fatalf("milestone/plan input is not exactly {key}: %#v", input)
	}
	output := schemaProperties(entry.OutputSchema)
	markdown, ok := output["markdown"].(map[string]any)
	if !ok || markdown["maxLength"] != 32768 {
		t.Fatalf("milestone/plan output is not bounded Markdown: %#v", output)
	}
	if err := server.actionContractSet().ValidateInput(tsk663MilestonePlanAction, map[string]any{"key": "GTW-MIL1"}); err != nil {
		t.Fatalf("compiled input rejected canonical key: %v", err)
	}
	if err := server.actionContractSet().ValidateInput(tsk663MilestonePlanAction, map[string]any{"milestone": "GTW-MIL1"}); err == nil {
		t.Fatal("compiled input accepted a noncanonical selector")
	}
	if err := server.actionContractSet().ValidateOutput(tsk663MilestonePlanAction, map[string]any{"markdown": "# GTW-MIL1 — Roadmap\n"}); err != nil {
		t.Fatalf("compiled output rejected Markdown: %v", err)
	}
	if err := server.actionContractSet().ValidateOutput(tsk663MilestonePlanAction, map[string]any{"markdown": strings.Repeat("x", 32769)}); err == nil {
		t.Fatal("compiled output accepted Markdown over its bound")
	}
}
