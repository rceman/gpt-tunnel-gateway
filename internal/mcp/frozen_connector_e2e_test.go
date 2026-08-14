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

func TestFrozenConnectorDiscoversAndInvokesRuntimeActionWithoutReconnect(t *testing.T) {
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

	client := &frozenConnectorClient{
		http:     httpServer.Client(),
		endpoint: httpServer.URL + "/mcp",
		methods:  map[string]int{},
	}
	initialized := client.request(t, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "frozen-test", "version": "1"}})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	client.notify(t, "notifications/initialized")
	tools := client.request(t, "tools/list", map[string]any{})
	if len(tools["result"].(map[string]any)["tools"].([]any)) == 0 {
		t.Fatal("initial tools/list was empty")
	}
	started := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "session_start", "arguments": map[string]any{"role": durableSession.RoleDelivery},
	}))
	sessionID := started["session"].(string)
	bound := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "session_update", "arguments": map[string]any{"session": sessionID, "project_id": "example"},
	}))
	if bound["is_error"] == true {
		t.Fatalf("session bind failed: %#v", bound)
	}
	if bound["project_id"] != "example" || bound["rules_acknowledgement_required"] != true {
		t.Fatalf("unexpected session update: %#v", bound)
	}
	rules := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": "rules/read", "input": map[string]any{}},
	}))
	if rules["is_error"] == true {
		t.Fatalf("rules read failed: %#v", rules)
	}

	rootBefore := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "schema", "arguments": map[string]any{"path": ""},
	}))
	for _, domain := range rootBefore["domains"].([]any) {
		if domain == "frozen" {
			t.Fatal("runtime action was available before deployment")
		}
	}
	connectionsBeforeDeploy := connections.Load()

	var executions atomic.Int32
	if err := server.RegisterGenericAction(GenericAction{
		Path:          "frozen/probe",
		Description:   "Runtime action deployed after connector initialization.",
		AuthorityRole: durableSession.RoleDelivery,
		InputSchema:   obj(map[string]any{"project_id": str("Bound session project"), "value": str("Probe value")}, "project_id", "value"),
		OutputSchema:  closedOutput(map[string]any{"project_id": outputString(), "value": outputString()}, "project_id", "value"),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				ProjectID string `json:"project_id"`
				Value     string `json:"value"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			executions.Add(1)
			return map[string]any{"project_id": input.ProjectID, "value": input.Value}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	contract := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "schema", "arguments": map[string]any{"path": "frozen/probe"},
	}))
	if contract["kind"] != "action" || contract["path"] != "frozen/probe" {
		t.Fatalf("deployed action was not discovered through schema: %#v", contract)
	}
	if client.methods["initialize"] != 1 || client.methods["tools/list"] != 1 || connections.Load() != connectionsBeforeDeploy {
		t.Fatalf("connector was refreshed or reconnected: methods=%v connections=%d before=%d", client.methods, connections.Load(), connectionsBeforeDeploy)
	}

	call := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name":      "call",
		"arguments": map[string]any{"session": sessionID, "action": "frozen/probe", "input": map[string]any{"value": "call"}},
	}))
	if _, ok := call["action"]; ok || call["is_error"] != false || call["result"].(map[string]any)["value"] != "call" {
		t.Fatalf("deployed action call failed: %#v", call)
	}

	batch := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "batch",
		"arguments": map[string]any{"session": sessionID, "calls": []any{
			map[string]any{"action": "frozen/probe", "input": map[string]any{"value": "batch"}},
		}},
	}))
	results := batch["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["is_error"] != false || results[0].(map[string]any)["result"].(map[string]any)["value"] != "batch" {
		t.Fatalf("deployed action batch failed: %#v", batch)
	}

	wrongProject := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name":      "call",
		"arguments": map[string]any{"session": sessionID, "action": "frozen/probe", "input": map[string]any{"project_id": "other", "value": "blocked"}},
	}))
	if wrongProject["is_error"] != true || executions.Load() != 2 {
		t.Fatalf("session project authority was bypassed: result=%#v executions=%d", wrongProject, executions.Load())
	}
}
