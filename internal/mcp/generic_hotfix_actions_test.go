package mcp

import (
	"reflect"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestHotfixActionsAreSessionBoundAndIntegrateRefIsExact(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{StateDir: t.TempDir()}, nil)}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"hotfix/create", "hotfix/integrate"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing generic action %q", path)
		}
		if !entry.SessionBound || !entry.SessionRequired {
			t.Fatalf("hotfix action %q is not session-bound: %#v", path, entry)
		}
	}
	entry := entries["hotfix/integrate"]
	properties := entry.InputSchema["properties"].(map[string]any)
	ref := properties["hotfix_ref"].(map[string]any)
	if ref["pattern"] != "^refs/heads/hotfix/[a-z0-9]+(?:-[a-z0-9]+)*$" || ref["minLength"] != 19 || ref["maxLength"] != 98 {
		t.Fatalf("hotfix_ref schema=%#v", ref)
	}
	if _, ok := properties["base_sha"]; ok {
		t.Fatal("integrate schema accepts caller-controlled base_sha")
	}
	if got := entry.InputSchema["required"]; !reflect.DeepEqual(got, []string{"hotfix_ref", "reviewed_sha"}) {
		t.Fatalf("required=%#v", got)
	}
}
