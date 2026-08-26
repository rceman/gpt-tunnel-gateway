package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTaskExecutionSchemasAreTaskIdentityOnly(t *testing.T) {
	work := taskWorkSchema()
	finalize := taskFinalizeSchema()
	for name, schema := range map[string]map[string]any{"work": work, "finalize": finalize} {
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema has no properties", name)
		}
		if _, ok := properties["completion_file"]; ok {
			t.Fatalf("%s exposes completion_file", name)
		}
	}
	finalizeProperties := finalize["properties"].(map[string]any)
	if _, ok := finalizeProperties["task_id"]; !ok {
		t.Fatal("finalize does not expose task_id")
	}
	if _, ok := finalizeProperties["summary"]; !ok {
		t.Fatal("finalize does not expose bounded semantic summary")
	}
	required, ok := finalize["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "task_id" {
		t.Fatalf("finalize required fields=%#v, want task_id only", finalize["required"])
	}
}

func TestTaskFinalizeStatusActionIsRegisteredAsLocalReceiptRead(t *testing.T) {
	server := &Server{Service: service.New(config.Config{StateDir: t.TempDir()})}
	entry, ok := server.genericActionRegistry(server.tools())["task/finalize_status"]
	if !ok {
		t.Fatal("task/finalize_status is not registered")
	}
	if !entry.LocalReceiptOnly || entry.AuthorityRole != actionRolePlannerOrDelivery {
		t.Fatalf("task/finalize_status has unsafe authority contract: %#v", entry.GenericAction)
	}
	properties, ok := entry.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("task/finalize_status input is not operation-only: %#v", entry.InputSchema)
	}
	if _, ok := properties["operation_id"]; !ok {
		t.Fatalf("task/finalize_status input exposes unexpected fields: %#v", properties)
	}
	if _, ok := properties["project_id"]; ok {
		t.Fatalf("task/finalize_status input exposes project_id: %#v", properties)
	}
	if _, ok := properties["task_id"]; ok {
		t.Fatalf("task/finalize_status input exposes task_id: %#v", properties)
	}
}
