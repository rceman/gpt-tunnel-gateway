package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestAgentSessionRoleBypassIsOnlyPromptRead(t *testing.T) {
	server := &Server{Service: service.New(config.Config{StateDir: filepath.Join(t.TempDir(), "state")})}
	entry := genericActionEntry{GenericAction: GenericAction{
		Path:             "agent/prompt_read",
		Description:      "Agent prompt read security test action.",
		InputSchema:      map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		OutputSchema:     closedOutput(map[string]any{"ok": outputBoolean()}, "ok"),
		LocalReceiptOnly: true,
		Execute:          func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
	}}
	entries := map[string]genericActionEntry{
		"agent/prompt_read": entry,
		"agent/other_receipt": {GenericAction: GenericAction{
			Path:             "agent/other_receipt",
			Description:      "Second Agent local-receipt security test action.",
			InputSchema:      entry.InputSchema,
			OutputSchema:     entry.OutputSchema,
			LocalReceiptOnly: true,
			Execute:          entry.Execute,
		}},
	}
	record := durableSession.Record{ID: "SA-AGENT001", Role: durableSession.RoleAgent, ProjectID: "example"}

	allowed, err := server.genericDispatch(context.Background(), entries, record, "agent/prompt_read", json.RawMessage(`{}`))
	if err != nil || allowed["is_error"] == true {
		t.Fatalf("agent/prompt_read was not allowed through its narrow bypass: result=%#v err=%v", allowed, err)
	}
	rejected, err := server.genericDispatch(context.Background(), entries, record, "agent/other_receipt", json.RawMessage(`{}`))
	if err != nil || rejected["is_error"] != true {
		t.Fatalf("another Agent local-receipt action bypassed the role guard: result=%#v err=%v", rejected, err)
	}
}
