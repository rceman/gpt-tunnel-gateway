package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestCallbackActionsAreRemovedInFavorOfConfigurationHooks(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"callback/events", "callback/list", "callback/register", "callback/remove", "gateway/capabilities", "gateway/status", "project/guide_bind"} {
		if _, ok := entries[path]; ok {
			t.Fatalf("retired callback action %q remains registered", path)
		}
	}
	for _, path := range []string{
		"config/procedure_list", "config/procedure_read", "config/procedure_create", "config/procedure_update", "config/procedure_remove",
		"config/hook_list", "config/hook_read", "config/hook_bind", "config/hook_unbind", "config/guide_bind", "runtime/status",
	} {
		if _, ok := entries[path]; !ok {
			t.Fatalf("missing canonical configuration action %q", path)
		}
	}
	runtimeStatus := entries["runtime/status"]
	runtimeStatusRequired := stringList(runtimeStatus.OutputSchema["required"])
	if runtimeStatus.InputSchema["additionalProperties"] != false || runtimeStatus.OutputSchema["additionalProperties"] != false || len(runtimeStatusRequired) != 4 {
		t.Fatalf("runtime/status is not a closed canonical contract: %#v", runtimeStatus)
	}
	for _, required := range []string{"service", "gateway", "time", "runtime_identity"} {
		if !containsRequired(runtimeStatusRequired, required) {
			t.Fatalf("runtime/status contract lacks %q: %#v", required, runtimeStatusRequired)
		}
	}
	for _, path := range []string{"config/procedure_create", "config/procedure_update", "config/procedure_remove", "config/hook_bind", "config/hook_unbind", "config/guide_bind"} {
		entry := entries[path]
		properties := schemaProperties(entry.InputSchema)
		if entry.InputSchema["additionalProperties"] != false || len(properties) == 0 || !containsRequired(stringList(entry.InputSchema["required"]), "reason") {
			t.Fatalf("configuration mutation %s is not closed with mandatory reason: %#v", path, entry.InputSchema)
		}
		if _, ok := properties["expected_revision"]; ok {
			t.Fatalf("configuration mutation %s exposes caller-owned CAS", path)
		}
		outputProperties := schemaProperties(entry.OutputSchema)
		if entry.OutputSchema["additionalProperties"] != false || len(outputProperties) != 1 || outputProperties["revision"] == nil || !reflectStringList(stringList(entry.OutputSchema["required"]), []string{"revision"}) {
			t.Fatalf("configuration mutation %s output is not the compact revision receipt: %#v", path, entry.OutputSchema)
		}
	}
}

func TestDynamicProcedureNamedReadIsDiscoveredAndCallable(t *testing.T) {
	server := newSessionTestServer(t)
	planner := genericSession(t, server.Service, "example")
	root := server.Service.Config.Projects["example"].Root
	scriptPath := filepath.Join(root, "scripts", "read.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '{\"ok\":true}' > \"$GTW_PROCEDURE_OUTPUT_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	definition := map[string]any{
		"script": "scripts/read.sh", "summary": "Read through a named Procedure", "guide": "Returns a structured success result.",
		"input":  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"output": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false},
	}
	if _, ok := tsk532CallResult(t, server, planner, "config/procedure_create", map[string]any{"name": "read", "definition": definition, "reason": "Test the reserved-looking Procedure name."}); !ok {
		t.Fatal("Procedure create action failed")
	}
	catalog, ok := tsk532CallResult(t, server, planner, "config/procedure_list", map[string]any{})
	if !ok {
		t.Fatal("Procedure catalogue action failed")
	}
	if len(catalog["procedures"].([]any)) != 1 {
		t.Fatalf("authoritative Procedure catalogue=%#v", catalog)
	}
	domain, err := server.genericSchemaPublic(server.AuthorityContext, server.tools(), mustJSON(t, map[string]any{"session": planner, "path": "procedure"}))
	if err != nil {
		t.Fatal(err)
	}
	domainResult := domain.(map[string]any)
	if domainResult["kind"] != "domain" || len(domainResult["actions"].([]any)) != 0 {
		t.Fatalf("procedure domain disclosed a catalogue: %#v", domainResult)
	}
	contract, err := server.genericSchemaPublic(server.AuthorityContext, server.tools(), mustJSON(t, map[string]any{"session": planner, "path": "procedure/read"}))
	if err != nil {
		t.Fatal(err)
	}
	contractJSON, err := json.Marshal(contract)
	if err != nil || strings.Contains(string(contractJSON), "scripts/read.sh") {
		t.Fatalf("dynamic Procedure contract leaked internal execution configuration: %s err=%v", contractJSON, err)
	}
	call, err := server.genericCallPublic(server.AuthorityContext, server.tools(), mustJSON(t, map[string]any{
		"session": planner, "action": "procedure/read", "input": map[string]any{},
	}))
	if err != nil {
		t.Fatal(err)
	}
	callResult, ok := call.(map[string]any)
	if !ok || callResult["ok"] != true {
		t.Fatalf("dynamic Procedure call=%#v", call)
	}
	result, ok := callResult["result"].(map[string]any)
	if !ok || result["status"] != "accepted" && result["status"] != "running" {
		t.Fatalf("Procedure call did not allocate its durable Operation: %#v", call)
	}
	operationID, ok := result["operation"].(string)
	if !ok || operationID == "" {
		t.Fatalf("Procedure call Operation=%#v", result)
	}
	await, err := server.Service.OperationAwait(service.WithAgentSessionID(context.Background(), planner), operationID, 5*time.Second)
	if err != nil || await.Status != "completed" {
		t.Fatalf("Procedure Operation receipt=%#v err=%v", await, err)
	}
}
