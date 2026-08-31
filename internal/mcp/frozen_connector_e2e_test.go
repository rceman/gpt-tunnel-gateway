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
	return c.requestWithMeta(t, method, params, nil)
}

func (c *frozenConnectorClient) requestWithMeta(t *testing.T, method string, params any, meta map[string]any) map[string]any {
	t.Helper()
	c.nextID++
	c.methods[method]++
	if meta != nil {
		object, ok := params.(map[string]any)
		if !ok {
			t.Fatalf("MCP %s params are not an object: %#v", method, params)
		}
		copied := make(map[string]any, len(object)+1)
		for key, value := range object {
			copied[key] = value
		}
		copied["_meta"] = meta
		params = copied
	}
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

func TestADR84FrozenConnectorContract(t *testing.T) {
	server := newSessionTestServer(t)
	httpServer := httptest.NewUnstartedServer(server.Router())
	httpServer.Start()
	defer httpServer.Close()

	client := &frozenConnectorClient{
		http:     httpServer.Client(),
		endpoint: httpServer.URL + "/mcp",
		methods:  map[string]int{},
	}
	initialized := client.request(t, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "adr84-test", "version": "1"}})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	client.notify(t, "notifications/initialized")
	tools := client.request(t, "tools/list", map[string]any{})
	rawTools := tools["result"].(map[string]any)["tools"].([]any)
	wantTools := map[string]bool{"status": true, "guide": true, "projects": true, "session_start": true, "schema": true, "call": true}
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
	if _, ok := status["ready"].(bool); !ok || len(status["gateways"].([]any)) != 1 || status["captured_at"] == "" {
		t.Fatalf("status is incomplete: %#v", status)
	}
	started := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "session_start", "arguments": map[string]any{"gateway": "test_gateway", "project": "EXM", "role": durableSession.RolePlanner, "ref": "connector"},
	}))
	sessionID := started["session"].(string)
	record, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil || record.ProjectID != "example" || record.Role != durableSession.RolePlanner || record.Status != durableSession.StatusActive || record.SessionRef == nil || *record.SessionRef != "connector" {
		t.Fatalf("session_start did not create the bound Planner session: %#v err=%v", record, err)
	}
	for _, path := range []string{"", "project", "project/status"} {
		contract := frozenResult(t, client.request(t, "tools/call", map[string]any{
			"name": "schema", "arguments": map[string]any{"session": sessionID, "path": path},
		}))
		if contract["path"] != path {
			t.Fatalf("schema(%q) returned %#v", path, contract)
		}
	}
	updateContract := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "session/update"},
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
	if call["ok"] != true {
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
	unknownResult := frozenResult(t, unknownResponse)
	if unknownResult["ok"] != false {
		t.Fatalf("unknown session was accepted: %#v", unknownResponse)
	}
	retired := client.request(t, "tools/call", map[string]any{
		"name": "session_update", "arguments": map[string]any{},
	})
	if retired["error"] == nil {
		t.Fatalf("session_update remained callable: %#v", retired)
	}
	retiredBatch := client.request(t, "tools/call", map[string]any{
		"name": "batch", "arguments": map[string]any{},
	})
	if retiredBatch["error"] == nil {
		t.Fatalf("retired batch tool remained callable: %#v", retiredBatch)
	}
}

func TestADR84RuntimeActionDoesNotRefreshConnector(t *testing.T) {
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
	initialized := client.request(t, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "adr84-no-refresh", "version": "1"}})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	client.notify(t, "notifications/initialized")
	client.request(t, "tools/list", map[string]any{})
	started := frozenResult(t, client.request(t, "tools/call", map[string]any{"name": "session_start", "arguments": map[string]any{"gateway": "test_gateway", "project": "EXM", "role": durableSession.RolePlanner}}))
	sessionID := started["session"].(string)
	connectionsBefore := connections.Load()
	if err := server.RegisterGenericAction(GenericAction{
		Path: "frozen/runtime_probe", Description: "Runtime action registered after connector bootstrap.", AuthorityRole: durableSession.RolePlanner,
		InputSchema: obj(map[string]any{}), OutputSchema: closedOutput(map[string]any{"ok": outputBoolean()}, "ok"),
		Execute: func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	contract := frozenResult(t, client.request(t, "tools/call", map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "frozen/runtime_probe"}}))
	if contract["path"] != "frozen/runtime_probe" {
		t.Fatalf("runtime action was not discovered: %#v", contract)
	}
	call := frozenResult(t, client.request(t, "tools/call", map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "frozen/runtime_probe", "input": map[string]any{}}}))
	if call["ok"] != true || call["result"].(map[string]any)["ok"] != true {
		t.Fatalf("runtime action call failed: %#v", call)
	}
	if client.methods["initialize"] != 1 || client.methods["tools/list"] != 1 || connections.Load() != connectionsBefore {
		t.Fatalf("connector refreshed or reconnected: methods=%v connections=%d before=%d", client.methods, connections.Load(), connectionsBefore)
	}
}
