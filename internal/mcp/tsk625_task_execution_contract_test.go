package mcp

import "testing"

func TestTSK625TaskStatusCarriesOptionalBlockedReason(t *testing.T) {
	server := newSessionTestServer(t)
	status, ok := server.genericActionRegistry(server.tools())["task/status"]
	if !ok {
		t.Fatal("task/status is not registered")
	}
	properties, ok := status.OutputSchema["properties"].(map[string]any)
	if !ok || properties["reason"] == nil || status.OutputSchema["additionalProperties"] != false {
		t.Fatalf("task/status reason schema=%#v", status.OutputSchema)
	}
	for _, required := range status.OutputSchema["required"].([]string) {
		if required == "reason" {
			t.Fatal("blocked reason must be optional in task/status output")
		}
	}
}

func TestTSK625TaskBlockResumeHaveClosedBoundedPlannerContracts(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"task/block", "task/resume"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("%s is not registered", path)
		}
		if entry.AuthorityRole != actionRolePlannerOrLead || !entry.SessionBound || !entry.LocalReceiptOnly {
			t.Fatalf("%s authority binding=%#v", path, entry)
		}
		if entry.InputSchema["additionalProperties"] != false || entry.ExecutionInputSchema["additionalProperties"] != false {
			t.Fatalf("%s input schema is not closed", path)
		}
		properties, ok := entry.InputSchema["properties"].(map[string]any)
		if !ok || len(properties) != 2 {
			t.Fatalf("%s input properties=%#v", path, entry.InputSchema)
		}
		if _, ok := properties["key"]; !ok {
			t.Fatalf("%s input omitted key", path)
		}
		reason, ok := properties["reason"].(map[string]any)
		if !ok || reason["minLength"] != 1 || reason["maxLength"] != 1024 {
			t.Fatalf("%s reason schema=%#v", path, reason)
		}
		outputProperties, ok := entry.OutputSchema["properties"].(map[string]any)
		if !ok || outputProperties["reason"] == nil || entry.OutputSchema["additionalProperties"] != false {
			t.Fatalf("%s output schema=%#v", path, entry.OutputSchema)
		}
	}
}
