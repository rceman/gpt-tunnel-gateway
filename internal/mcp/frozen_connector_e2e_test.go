package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type frozenConnectorClient struct {
	http     *http.Client
	endpoint string
	nextID   int
	methods  map[string]int
}

func (c *frozenConnectorClient) request(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	c.nextID++
	c.methods[method]++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP %s status=%d body=%s", method, resp.StatusCode, data)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("MCP %s response: %v: %s", method, err, data)
	}
	return decoded
}

func (c *frozenConnectorClient) notify(t *testing.T, method string) {
	t.Helper()
	c.methods[method]++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("MCP notification %s status=%d", method, resp.StatusCode)
	}
}

func frozenResult(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP result: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP structured content: %#v", response)
	}
	return structured
}

func TestADR74FrozenConnectorContract(t *testing.T) {
	server := newSessionTestServer(t)
	httpServer := httptest.NewUnstartedServer(server.Router())
	httpServer.Start()
	defer httpServer.Close()

	client := &frozenConnectorClient{
		http:     httpServer.Client(),
		endpoint: httpServer.URL + "/mcp",
		methods:  map[string]int{},
	}
	initialized := client.request(t, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "adr74-test", "version": "1"}})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	client.notify(t, "notifications/initialized")
	tools := client.request(t, "tools/list", map[string]any{})
	rawTools := tools["result"].(map[string]any)["tools"].([]any)
	wantTools := map[string]bool{"status": true, "session_start": true, "schema": true, "call": true, "batch": true}
	if len(rawTools) != len(wantTools) {
		t.Fatalf("tools/list=%#v", rawTools)
	}
	for _, raw := range rawTools {
		name := raw.(map[string]any)["name"].(string)
		if !wantTools[name] || name == "session_update" {
			t.Fatalf("unexpected public tool %q", name)
		}
		delete(wantTools, name)
	}
	if len(wantTools) != 0 {
		t.Fatalf("missing public tools=%v", wantTools)
	}
	status := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "status", "arguments": map[string]any{},
	}))
	if status["status"] == "" || status["recommended_next_action"] == "" {
		t.Fatalf("status is incomplete: %#v", status)
	}
	if _, ok := status["time"]; ok {
		t.Fatal("status exposed non-canonical time")
	}
	registered := status["registered_projects"].(map[string]any)
	if _, ok := registered["has_more"].(bool); !ok {
		t.Fatalf("status omitted registered-project has_more: %#v", status)
	}
	started := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "session_start", "arguments": map[string]any{"role": durableSession.RolePlanner, "label": "connector"},
	}))
	sessionID := started["session"].(string)
	if _, ok := started["recommended_next_action"].(string); !ok {
		t.Fatalf("session_start omitted recommended_next_action: %#v", started)
	}
	if _, ok := started["next_steps"]; ok {
		t.Fatal("session_start returned deprecated next_steps")
	}
	record, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil || record.ProjectID != "" || record.Role != durableSession.RolePlanner || record.Status != durableSession.StatusActive || record.Label == nil || *record.Label != "connector" {
		t.Fatalf("session_start did not create an unbound Planner session: %#v err=%v", record, err)
	}
	frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": "session/update", "input": map[string]any{"project_id": "example"}},
	}))
	record, err = durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil || record.ProjectID != "example" {
		t.Fatalf("session/bind did not bind session: %#v err=%v", record, err)
	}
	for _, path := range []string{"", "project", "project/status"} {
		contract := frozenResult(t, client.request(t, "tools/call", map[string]any{
			"name": "schema", "arguments": map[string]any{"path": path},
		}))
		if contract["path"] != path {
			t.Fatalf("schema(%q) returned %#v", path, contract)
		}
	}
	updateContract := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "schema", "arguments": map[string]any{"path": "session/update"},
	}))
	if updateContract["path"] != "session/update" {
		t.Fatalf("session/update was not discoverable as an application action: %#v", updateContract)
	}
	frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": "session/update", "input": map[string]any{"label": "updated"}},
	}))
	updatedRecord, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil || updatedRecord.Label == nil || *updatedRecord.Label != "updated" {
		t.Fatalf("session/update did not update the bound session: %#v err=%v", updatedRecord, err)
	}
	call := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": "rules/read", "input": map[string]any{}},
	}))
	if call["is_error"] == true {
		t.Fatalf("call failed: %#v", call)
	}
	missing := client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"action": "rules/read", "input": map[string]any{}},
	})
	if missing["error"] == nil {
		t.Fatalf("missing call session was accepted: %#v", missing)
	}
	unknownResponse := client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": "SP-INVALID1", "action": "rules/read", "input": map[string]any{}},
	})
	unknownResult := unknownResponse["result"].(map[string]any)
	if unknownResult["isError"] != true {
		t.Fatalf("unknown session was accepted: %#v", unknownResponse)
	}
	retired := client.request(t, "tools/call", map[string]any{
		"name": "session_update", "arguments": map[string]any{},
	})
	if retired["error"] == nil {
		t.Fatalf("session_update remained callable: %#v", retired)
	}
	batch := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "batch",
		"arguments": map[string]any{"session": sessionID, "calls": []any{
			map[string]any{"action": "rules/read", "input": map[string]any{}},
			map[string]any{"action": "rules/read", "input": map[string]any{}},
		}},
	}))
	results := batch["results"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["action"] != "rules/read" || results[1].(map[string]any)["action"] != "rules/read" {
		t.Fatalf("deployed action batch failed: %#v", batch)
	}
}

func TestADR74V1RuntimeActionDoesNotRefreshConnector(t *testing.T) {
	server := newSessionTestServer(t)
	var connections atomic.Int32
	httpServer := httptest.NewUnstartedServer(server.Router())
	httpServer.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	httpServer.Start()
	defer httpServer.Close()
	client := &frozenConnectorClient{http: httpServer.Client(), endpoint: httpServer.URL + "/mcp", methods: map[string]int{}}
	initialized := client.request(t, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "adr74-no-refresh", "version": "1"}})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	client.notify(t, "notifications/initialized")
	client.request(t, "tools/list", map[string]any{})
	started := frozenResult(t, client.request(t, "tools/call", map[string]any{"name": "session_start", "arguments": map[string]any{"role": durableSession.RolePlanner}}))
	sessionID := started["session"].(string)
	connectionsBefore := connections.Load()
	if err := server.RegisterGenericAction(GenericAction{
		Path: "frozen/runtime_probe", Description: "Runtime action registered after connector bootstrap.", AuthorityRole: durableSession.RolePlanner,
		InputSchema: obj(map[string]any{}), OutputSchema: closedOutput(map[string]any{"ok": outputBoolean()}, "ok"),
		Execute: func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	frozenResult(t, client.request(t, "tools/call", map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "session/update", "input": map[string]any{"project_id": "example"}}}))
	contract := frozenResult(t, client.request(t, "tools/call", map[string]any{"name": "schema", "arguments": map[string]any{"path": "frozen/runtime_probe"}}))
	if contract["path"] != "frozen/runtime_probe" {
		t.Fatalf("runtime action was not discovered: %#v", contract)
	}
	call := frozenResult(t, client.request(t, "tools/call", map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "frozen/runtime_probe", "input": map[string]any{}}}))
	if call["is_error"] != false || call["result"].(map[string]any)["ok"] != true {
		t.Fatalf("runtime action call failed: %#v", call)
	}
	if client.methods["initialize"] != 1 || client.methods["tools/list"] != 1 || connections.Load() != connectionsBefore {
		t.Fatalf("connector refreshed or reconnected: methods=%v connections=%d before=%d", client.methods, connections.Load(), connectionsBefore)
	}
}
