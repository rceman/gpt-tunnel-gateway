package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func newCallbackPublicServer(t *testing.T) (*Server, *sqlitestore.Databases) {
	t.Helper()
	server := newSessionTestServer(t)
	db, err := sqlitestore.Open(server.Service.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	configuration := model.DefaultProjectConfiguration("example", now)
	payload, err := json.Marshal(configuration)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{OperationID: "seed-public-callback-config", EntityType: "project_configuration", EntityID: "example", ExpectedRevision: 0, Revision: 1, Kind: "seed", Payload: payload, CreatedAt: now, Create: true}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.MarkSharedBootstrapComplete(context.Background(), sqlitestore.SharedBootstrapMarker{ProjectID: "example", HubRevision: "fixture", CompletedAt: now.Format(time.RFC3339Nano)}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	c := server.Service.Config
	project := c.Projects["example"]
	project.ProjectCode = "EXM"
	c.Projects["example"] = project
	server.Service = service.NewWithDurabilityDeferredWorkers(c, db)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	return server, db
}

func publicCallbackCall(t *testing.T, server *Server, session, action string, input map[string]any) map[string]any {
	t.Helper()
	return genericActionResult(t, publicCallbackEnvelope(t, server, session, action, input))
}

func publicCallbackEnvelope(t *testing.T, server *Server, session, action string, input map[string]any) map[string]any {
	t.Helper()
	return callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session": session, "action": action, "input": input}, "_meta": callbackADR81Metadata(t, "ADR81 traces the public callback schema, decoder and dispatch path for the ADR87 callback registry using ADR83 typed projection naming.")},
	}))
}

func callbackADR81Metadata(t *testing.T, why string) map[string]any {
	t.Helper()
	if strings.TrimSpace(why) == "" {
		t.Fatal("callback ADR81 metadata requires why")
	}
	return map[string]any{"adr": "GTW-ADR81", "references": []string{"GTW-ADR87", "GTW-ADR83"}, "why": why}
}

func TestCallbackActionsPublicSchemasAndLifecycle(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"callback/events", "callback/list", "callback/register", "callback/remove"} {
		if _, ok := entries[path]; !ok {
			t.Fatalf("missing callback action %q", path)
		}
	}
	if _, ok := entries["callback/read"]; ok {
		t.Fatal("retired callback/read was registered")
	}
	if required := stringList(entries["callback/register"].InputSchema["required"]); len(required) != 2 || required[0] != "callback" || required[1] != "event" {
		t.Fatalf("register required=%v", required)
	}
	if len(server.publicTools()) != len(canonicalToolManifest) {
		t.Fatal("callback actions leaked into top-level MCP tools")
	}
}

func TestCallbackActionsUsePublicCallAndSharedRegistry(t *testing.T) {
	server, db := newCallbackPublicServer(t)
	defer db.Close()
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "planner", "session_type": "chatgpt"}))
	session := started["session"].(map[string]any)["session_id"].(string)
	events := publicCallbackCall(t, server, session, "callback/events", map[string]any{})
	if len(events["events"].([]any)) != 1 {
		t.Fatalf("events=%#v", events)
	}
	list := publicCallbackCall(t, server, session, "callback/list", map[string]any{})
	if len(list["callbacks"].([]any)) != 0 {
		t.Fatalf("empty list=%#v", list)
	}
	registered := publicCallbackCall(t, server, session, "callback/register", map[string]any{
		"callback": "http-hook", "event": model.ProjectCallbackWorkFinishedEvent,
		"url": map[string]any{"method": "POST", "url": "https://example.invalid/hook", "body": "{}"},
	})
	if registered["status"] != "registered" {
		t.Fatalf("register=%#v", registered)
	}
	if registered["key"] != "http-hook" || registered["callback"] != nil {
		t.Fatalf("register response=%#v", registered)
	}
	repeated := publicCallbackCall(t, server, session, "callback/register", map[string]any{
		"callback": "http-hook", "event": model.ProjectCallbackWorkFinishedEvent,
		"url": map[string]any{"method": "POST", "url": "https://example.invalid/hook", "body": "{}"},
	})
	if repeated["status"] != "already_registered" {
		t.Fatalf("repeat=%#v", repeated)
	}
	scriptRegistered := publicCallbackCall(t, server, session, "callback/register", map[string]any{
		"callback": "script-hook", "event": model.ProjectCallbackWorkFinishedEvent,
		"script": map[string]any{"path": "scripts/callback", "args": []any{}},
	})
	if scriptRegistered["status"] != "registered" {
		t.Fatalf("script register=%#v", scriptRegistered)
	}
	combinedRegistered := publicCallbackCall(t, server, session, "callback/register", map[string]any{
		"callback": "combined-hook", "event": model.ProjectCallbackWorkFinishedEvent,
		"url":    map[string]any{"method": "PUT", "url": "https://example.invalid/combined", "body": "{}"},
		"script": map[string]any{"path": "scripts/combined", "args": []any{}},
	})
	if combinedRegistered["status"] != "registered" {
		t.Fatalf("combined register=%#v", combinedRegistered)
	}
	conflict := genericStructured(t, publicCallbackEnvelope(t, server, session, "callback/register", map[string]any{
		"callback": "http-hook", "event": model.ProjectCallbackWorkFinishedEvent,
		"url": map[string]any{"method": "PUT", "url": "https://example.invalid/hook", "body": "{}"},
	}))
	if conflict["is_error"] != true {
		t.Fatalf("conflict=%#v", conflict)
	}
	list = publicCallbackCall(t, server, session, "callback/list", map[string]any{})

	serialized, _ := json.Marshal(list)
	if string(serialized) == "" || string(serialized) == "{}" {
		t.Fatal("list did not return a compact result")
	}
	callbacks := list["callbacks"].([]any)
	if len(callbacks) != 3 {
		t.Fatalf("callback list=%#v", list)
	}
	for _, raw := range callbacks {
		entry := raw.(map[string]any)
		if _, ok := entry["body"]; ok {
			t.Fatalf("callback list leaked URL body: %#v", entry)
		}
		if _, ok := entry["args"]; ok {
			t.Fatalf("callback list leaked script args: %#v", entry)
		}
	}
	combined := callbacks[0].(map[string]any)
	if combined["key"] != "combined-hook" || combined["url"] == nil || combined["script"] == nil {
		t.Fatalf("combined callback summary=%#v", combined)
	}
	removed := publicCallbackCall(t, server, session, "callback/remove", map[string]any{"callback": "http-hook"})
	if removed["status"] != "removed" {
		t.Fatalf("remove=%#v", removed)
	}
	if removed["key"] != "http-hook" || removed["callback"] != nil {
		t.Fatalf("remove response=%#v", removed)
	}
	missing := genericStructured(t, publicCallbackEnvelope(t, server, session, "callback/remove", map[string]any{"callback": "http-hook"}))
	if missing["is_error"] != true {
		t.Fatalf("missing remove=%#v", missing)
	}
}

func TestCallbackActionsRejectDeliveryMutationAndRequireBoundSession(t *testing.T) {
	server, db := newCallbackPublicServer(t)
	defer db.Close()
	response := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"action": "callback/list", "input": map[string]any{}}}}))
	if response["error"] == nil && !strings.Contains(string(mustJSON(t, response)), "session") {
		t.Fatalf("unbound callback action was not rejected: %#v", response)
	}
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt"}))
	session := started["session"].(map[string]any)["session_id"].(string)
	result := genericStructured(t, publicCallbackEnvelope(t, server, session, "callback/register", map[string]any{"callback": "delivery", "event": model.ProjectCallbackWorkFinishedEvent, "url": map[string]any{"method": "POST", "url": "https://example.invalid", "body": "{}"}}))
	if result["is_error"] != true {
		t.Fatalf("delivery registered callback: %#v", result)
	}
}
