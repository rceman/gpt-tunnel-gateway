package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestAgentActionsAreGenericDiscoverableAndInvokable(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"agent/prompt", "agent/recover", "agent/interrupt", "agent/update", "agent/disable", "agent/read", "agent/list", "agent/status"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing agent generic action %s", path)
		}
		if entry.InputSchema == nil || entry.OutputSchema == nil || entry.Execute == nil {
			t.Fatalf("incomplete agent generic action %s", path)
		}
	}
	recovery := entries["agent/recover"]
	recoveryProperties := recovery.InputSchema["properties"].(map[string]any)
	for _, field := range []string{"train_id", "item_position", "task_id", "attempt_number", "agent_id"} {
		if _, ok := recoveryProperties[field]; !ok {
			t.Fatalf("agent/recover omitted exact Attempt field %s", field)
		}
	}
	for _, path := range []string{"agent/send", "agent/followup", "agent/force_prompt", "agent/redirect"} {
		if _, ok := entries[path]; ok {
			t.Fatalf("retired steering alias remains canonical: %s", path)
		}
	}
	interrupt := entries["agent/interrupt"]
	properties := interrupt.InputSchema["properties"].(map[string]any)
	if _, ok := properties["operation_id"]; !ok {
		t.Fatal("agent/interrupt omitted operation_id")
	}
	if _, ok := properties["message"]; !ok {
		t.Fatal("agent/interrupt omitted optional replacement message")
	}
	outputProperties := interrupt.OutputSchema["properties"].(map[string]any)
	for _, field := range []string{"operation_id", "status", "created_at", "updated_at"} {
		if _, ok := outputProperties[field]; !ok {
			t.Fatalf("agent/interrupt receipt omitted field %s", field)
		}
	}
	if _, ok := server.tools()["agent_register"]; ok {
		t.Fatal("agent registry action was incorrectly added as a typed tool")
	}
	entry := entries["agent/read"]
	if _, err := entry.Execute(authority.WithDelivery(context.Background()), json.RawMessage(`{"project_id":"missing","agent_id":"coder"}`)); err == nil {
		t.Fatal("agent/read unexpectedly succeeded for an unknown project")
	}
}

func TestAgentPromptOutputSchemaKeepsSuccessCompact(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	entry := server.genericActionRegistry(server.tools())["agent/prompt"]
	if err := validateSchemaValue(entry.OutputSchema, map[string]any{
		"operation_id": "mutation-test",
		"status":       "accepted",
		"created_at":   "2026-08-15T00:00:00Z",
		"updated_at":   "2026-08-15T00:00:00Z",
	}, "result"); err != nil {
		t.Fatalf("compact receipt rejected: %v", err)
	}
	for _, field := range []string{"started_at", "finished_at", "stdout", "stderr"} {
		result := map[string]any{"operation_id": "mutation-test", "status": "accepted", "created_at": "2026-08-15T00:00:00Z", "updated_at": "2026-08-15T00:00:00Z", field: "unexpected"}
		if err := validateSchemaValue(entry.OutputSchema, result, "result"); err == nil {
			t.Fatalf("receipt accepted retired field %s", field)
		}
	}
}
