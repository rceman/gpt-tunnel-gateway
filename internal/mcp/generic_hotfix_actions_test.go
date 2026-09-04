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
	create := entries["hotfix/create"]
	createProperties := create.InputSchema["properties"].(map[string]any)
	if _, ok := createProperties["task"]; !ok {
		t.Fatal("hotfix/create does not expose its required Task binding")
	}
	if got := create.InputSchema["required"]; !reflect.DeepEqual(got, []string{"slug", "task"}) {
		t.Fatalf("hotfix/create required=%#v", got)
	}
}

func TestHotfixInventoryReadsAreBoundedAndProjectBound(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{StateDir: t.TempDir()}, nil)}
	entries := server.genericActionRegistry(server.tools())
	list := entries["hotfix/list"]
	read := entries["hotfix/read"]
	if !list.SessionBound || !list.SessionRequired || !list.LocalReadOnly {
		t.Fatalf("hotfix/list contract=%#v", list)
	}
	if !read.SessionBound || !read.SessionRequired || !read.LocalReadOnly {
		t.Fatalf("hotfix/read contract=%#v", read)
	}
	for name, entry := range map[string]genericActionEntry{"hotfix/list": list, "hotfix/read": read} {
		properties := entry.InputSchema["properties"].(map[string]any)
		if _, ok := properties["project_id"]; ok {
			t.Fatalf("%s exposes caller project_id", name)
		}
	}
	if _, ok := list.OutputSchema["properties"].(map[string]any)["main_head"]; !ok {
		t.Fatal("hotfix/list output omits main_head")
	}
	readRequired := read.OutputSchema["required"]
	if !reflect.DeepEqual(readRequired, []string{"project_id", "hotfix_ref", "task_id", "base_sha", "head_sha", "materialized"}) {
		t.Fatalf("hotfix/read required=%#v", readRequired)
	}
}
