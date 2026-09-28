package mcp

import (
	"strings"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const tsk670TaskResetAction = "task/reset"

func TestTSK670TaskResetContractAndPlannerAuthority(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries[tsk670TaskResetAction]
	if !ok {
		t.Fatal("task/reset handler is not registered")
	}
	contract, ok := server.actionContractSet().Action(tsk670TaskResetAction)
	if !ok {
		t.Fatal("task/reset contract is not compiled")
	}
	if contract.Metadata.Selector != "key" || contract.Metadata.Annotations.ReadOnly || !contract.Metadata.Annotations.Destructive || !contract.Metadata.Annotations.Idempotent {
		t.Fatalf("task/reset contract metadata=%#v", contract.Metadata)
	}
	if !entry.SessionBound || !entry.SessionRequired || !entry.InjectSessionProjectID || !entry.LocalReceiptOnly || entry.AuthorityRole != durableSession.RolePlanner {
		t.Fatalf("task/reset authority=%#v", entry.GenericAction)
	}
	if entry.Annotations.ReadOnlyHint || !entry.Annotations.DestructiveHint || !entry.Annotations.IdempotentHint {
		t.Fatalf("task/reset annotations=%#v", entry.Annotations)
	}
	input := schemaProperties(entry.InputSchema)
	if len(input) != 2 || input["key"] == nil || input["reason"] == nil {
		t.Fatalf("task/reset input is not exactly {key, reason}: %#v", input)
	}
	output := schemaProperties(entry.OutputSchema)
	if len(output) != 3 || output["key"] == nil || output["status"] == nil || output["execution_revision"] == nil {
		t.Fatalf("task/reset output schema=%#v", output)
	}
	contracts := server.actionContractSet()
	valid := map[string]any{"key": "EXM-TSK670", "reason": "retire stale execution"}
	if err := contracts.ValidateInput(tsk670TaskResetAction, valid); err != nil {
		t.Fatalf("task/reset rejected canonical input: %v", err)
	}
	for name, invalid := range map[string]map[string]any{
		"missing key":      {"reason": "retire stale execution"},
		"missing reason":   {"key": "EXM-TSK670"},
		"extra field":      {"key": "EXM-TSK670", "reason": "retire stale execution", "project_id": "other"},
		"oversized reason": {"key": "EXM-TSK670", "reason": strings.Repeat("x", 1025)},
	} {
		if err := contracts.ValidateInput(tsk670TaskResetAction, invalid); err == nil {
			t.Errorf("task/reset accepted %s", name)
		}
	}
	if err := contracts.ValidateOutput(tsk670TaskResetAction, map[string]any{"key": "EXM-TSK670", "status": "planned", "execution_revision": 4}); err != nil {
		t.Fatalf("task/reset rejected a retired execution receipt: %v", err)
	}
	if err := contracts.ValidateOutput(tsk670TaskResetAction, map[string]any{"key": "EXM-TSK670", "status": "planned"}); err == nil {
		t.Fatal("task/reset accepted a receipt without an execution revision")
	}
	if err := contracts.ValidateOutput(tsk670TaskResetAction, map[string]any{"key": "EXM-TSK670", "status": "planned", "execution_revision": 4, "reason": "forbidden echo"}); err == nil {
		t.Fatal("task/reset output accepted an undeclared field")
	}
}
