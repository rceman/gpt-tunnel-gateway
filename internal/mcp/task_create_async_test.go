package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTaskCreateExposesDurableStatusAction(t *testing.T) {
	server := &Server{Service: service.New(config.Config{StateDir: t.TempDir()})}
	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{
			"path": "task/create_status",
		}},
	})))
	if contract["kind"] != "action" || contract["path"] != "task/create_status" {
		t.Fatalf("task/create_status missing from generic schema: %#v", contract)
	}
}

func TestTaskUpdateExposesDurableStatusAction(t *testing.T) {
	server := &Server{Service: service.New(config.Config{StateDir: t.TempDir()})}
	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{
			"path": "task/update_status",
		}},
	})))
	if contract["kind"] != "action" || contract["path"] != "task/update_status" {
		t.Fatalf("task/update_status missing from generic schema: %#v", contract)
	}
}

func TestTaskReadyExposesDurableStatusAction(t *testing.T) {
	server := &Server{Service: service.New(config.Config{StateDir: t.TempDir()})}
	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{
			"path": "task/ready_status",
		}},
	})))
	if contract["kind"] != "action" || contract["path"] != "task/ready_status" {
		t.Fatalf("task/ready_status missing from generic schema: %#v", contract)
	}
}
