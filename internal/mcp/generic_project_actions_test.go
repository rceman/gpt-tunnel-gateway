package mcp

import (
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestProjectConfigurationUpdateIsGenericAndStrictlyDiscoverable(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["project/update"]
	if !ok || entry.Execute == nil || entry.InputSchema == nil || entry.OutputSchema == nil {
		t.Fatalf("project/update generic action is incomplete: %#v", entry)
	}
	if _, ok := server.tools()["project_update"]; ok {
		t.Fatal("project configuration update was incorrectly added as a typed tool")
	}
	properties := entry.InputSchema["properties"].(map[string]any)
	if _, ok := properties["expected_revision"]; !ok {
		t.Fatal("project/update omits expected revision")
	}
	patch := properties["patch"].(map[string]any)
	if patch["additionalProperties"] != false {
		t.Fatal("project/update patch is not closed")
	}
	checkpoint, ok := patch["properties"].(map[string]any)["checkpoint"].(map[string]any)
	if !ok || checkpoint["additionalProperties"] != false {
		t.Fatal("project/update omits closed checkpoint patch")
	}
	if _, ok := checkpoint["properties"].(map[string]any)["adapter"]; !ok {
		t.Fatal("project/update checkpoint omits adapter")
	}
}

func TestProjectConfigurationUpdateDecoderAcceptsCheckpointPatch(t *testing.T) {
	var input service.ProjectConfigurationUpdateInput
	raw := json.RawMessage(`{"project_id":"gpt-tunnel-gateway","expected_revision":7,"patch":{"checkpoint":{"adapter":"go"}},"updated_by":"planner"}`)
	if err := decode(raw, &input); err != nil {
		t.Fatal(err)
	}
	if input.Patch.Checkpoint == nil || input.Patch.Checkpoint.Adapter != "go" {
		t.Fatalf("checkpoint patch was not decoded: %#v", input.Patch.Checkpoint)
	}
}

func TestProjectRemoveIsGenericDestructiveAndNotTyped(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["project/remove"]
	if !ok || entry.Execute == nil || entry.InputSchema == nil {
		t.Fatalf("project/remove generic action is incomplete: %#v", entry)
	}
	if entry.AuthorityRole != actionRolePlannerOrDelivery || entry.Annotations.DestructiveHint != true || entry.Annotations.IdempotentHint != true {
		t.Fatalf("project/remove authority/annotations = %#v", entry)
	}
	if _, ok := server.tools()["project_remove"]; ok {
		t.Fatal("project/remove was incorrectly added as a typed tool")
	}
	properties := entry.InputSchema["properties"].(map[string]any)
	if _, ok := properties["project_id"]; ok {
		t.Fatal("project/remove exposes caller-selectable project_id")
	}
}
