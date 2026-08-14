package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func genericSession(t *testing.T, s *service.Service, projectID string) string {
	return genericSessionWithRole(t, s, projectID, durableSession.RoleDelivery)
}

func genericSessionWithRole(t *testing.T, s *service.Service, projectID, role string) string {
	t.Helper()
	if s.Config.StateDir == "" {
		s.Config.StateDir = t.TempDir()
	}
	record, err := durableSession.NewStore(s.Config.StateDir).Create(durableSession.CreateInput{ProjectID: projectID, Role: role, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	return record.ID
}

func genericStructured(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP result: %#v", response)
	}
	if result["isError"] == true {
		t.Fatalf("MCP tool error: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured content: %#v", response)
	}
	return structured
}

func TestGenericSessionStartIsDiscoverableAndCreatesPlannerSession(t *testing.T) {
	server := newSessionTestServer(t)
	dispatch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "session", "arguments": map[string]any{
			"action": "start", "project_id": "example", "role": durableSession.RolePlanner, "session_type": durableSession.SessionTypeChatGPT,
		}},
	})))
	if dispatch["action"] != "start" {
		t.Fatalf("session.start public call failed: %#v", dispatch)
	}
	session := dispatch["session"].(map[string]any)
	if session["role"] != durableSession.RolePlanner || !strings.HasPrefix(session["session_id"].(string), "SP-") {
		t.Fatalf("generic planner bootstrap did not create SP session: %#v", session)
	}
}

func TestQueryRunUsesSharedReadOnlyDSLAndSchemaDiscovery(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": durableSession.RoleDelivery, "session_type": durableSession.SessionTypeChatGPT}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	adoptTestWorkflowPolicy(t, server.Service, "example", revision)
	rules := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 0, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "rules/read", "input": map[string]any{}}}})))
	if rules["is_error"] == true {
		t.Fatalf("rules/read failed after policy setup: %#v", rules)
	}
	result := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "query/run", "input": map[string]any{"dsl": "task.list().select(id,status).limit(2)"}}}})))
	queryResult := result["result"].(map[string]any)
	if _, ok := result["action"]; ok || result["is_error"] == true || queryResult["entity"] != "task" {
		t.Fatalf("query/run failed: %#v", result)
	}
	runContract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "query/run"}}})))
	if runContract["kind"] != "action" || runContract["path"] != "query/run" {
		t.Fatalf("query/run action was not discoverable: %#v", runContract)
	}
	runDefinition := runContract["contract"].(map[string]any)
	if runDefinition["path"] != "query/run" || runDefinition["annotations"].(map[string]any)["readOnlyHint"] != true {
		t.Fatalf("query/run contract is not read-only: %#v", runDefinition)
	}
	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "query/task"}}})))
	if contract["kind"] != "query_entity" || contract["contract"].(map[string]any)["entity"] != "task" {
		t.Fatalf("query schema=%#v", contract)
	}
	invalidSchema := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "query/missing"}}}))
	if !strings.Contains(string(mustJSON(t, invalidSchema)), "query schema path") || !strings.Contains(string(mustJSON(t, invalidSchema)), "query/missing") {
		t.Fatalf("invalid query entity was accepted: %#v", invalidSchema)
	}
	ordinary := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "task/read"}}})))
	if ordinary["kind"] != "action" || ordinary["path"] != "task/read" {
		t.Fatalf("ordinary action schema lookup failed: %#v", ordinary)
	}
	invalid := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "query/run", "input": map[string]any{"dsl": "task.read()"}}}}))
	if !strings.Contains(string(mustJSON(t, invalid)), "query must begin") {
		t.Fatalf("exact-read query was not rejected: %#v", invalid)
	}
}

func TestGenericTransportSchemasAreCompactAndApplicationIndependent(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	tools := server.tools()
	staticBytes := 0
	for _, name := range []string{"call", "schema", "batch"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("generic tool %q is not registered", name)
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		staticBytes += len(encoded)
		text := string(encoded)
		if strings.Contains(text, "project_id") || strings.Contains(text, "oneOf") || strings.Contains(text, "anyOf") || strings.Contains(text, "enum") {
			t.Fatalf("generic static schema embeds application contract: %s", text)
		}
	}
	t.Logf("generic static input-schema bytes=%d", staticBytes)
	root := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": ""}},
	})))
	if root["revision"] != genericSchemaRevision || root["kind"] != "root" {
		t.Fatalf("unexpected generic schema root: %#v", root)
	}
	if len(root["domains"].([]any)) == 0 {
		t.Fatal("generic schema root has no domains")
	}
	before := make([]Tool, 0, len(tools)-3)
	after := make([]Tool, 0, len(tools))
	for name, tool := range tools {
		tool.Execute = nil
		after = append(after, tool)
		if name != "call" && name != "schema" && name != "batch" {
			before = append(before, tool)
		}
	}
	beforeBytes, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("static MCP manifest bytes before=%d after=%d additive=%d token-estimate-before=%d after=%d", len(beforeBytes), len(afterBytes), len(afterBytes)-len(beforeBytes), (len(beforeBytes)+3)/4, (len(afterBytes)+3)/4)
}

func TestGenericRegisteredActionDiscoveryCallAndBatchFailureContinuation(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc", StateDir: filepath.Join(t.TempDir(), "state")}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	if err := server.RegisterGenericAction(GenericAction{
		Path:        "test/echo",
		Description: "Echo one value for transport testing.",
		InputSchema: obj(map[string]any{"value": str("Value")}, "value"),
		OutputSchema: closedOutput(map[string]any{
			"value": outputString(),
		}, "value"),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				Value string `json:"value"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return map[string]any{"value": input.Value}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "test/echo"}},
	})))
	if contract["kind"] != "action" || contract["path"] != "test/echo" {
		t.Fatalf("unexpected action contract: %#v", contract)
	}

	call := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/echo", "input": map[string]any{"value": "ok"}}},
	})))
	if _, ok := call["action"]; ok || call["is_error"] != false {
		t.Fatalf("unexpected generic call: %#v", call)
	}
	invalid := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/echo", "input": map[string]any{"wrong": true}}},
	})))
	if _, ok := invalid["action"]; ok || invalid["is_error"] != true || !strings.Contains(invalid["result"].(map[string]any)["error"].(string), `schema with path="test/echo"`) {
		t.Fatalf("generic validation error was not actionable: %#v", invalid)
	}

	batch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "batch", "arguments": map[string]any{"session_id": sessionID, "calls": []any{
			map[string]any{"action": "test/echo", "input": map[string]any{"value": "first"}},
			map[string]any{"action": "missing/action", "input": map[string]any{}},
			map[string]any{"action": "test/echo", "input": map[string]any{"value": "last"}},
		}}},
	})))
	results := batch["results"].([]any)
	if len(results) != 3 || results[0].(map[string]any)["action"] != "test/echo" || results[1].(map[string]any)["action"] != "missing/action" || results[1].(map[string]any)["is_error"] != true || results[2].(map[string]any)["action"] != "test/echo" || results[2].(map[string]any)["is_error"] != false {
		t.Fatalf("batch did not preserve ordered continuation: %#v", batch)
	}
}

func TestGenericLegacyReadAndMutationAuthorityReuse(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "home_pc", StateDir: filepath.Join(t.TempDir(), "state")}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	sessionID := genericSession(t, server.Service, "example")
	generic := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "system/ping", "input": map[string]any{}}},
	})))
	if generic["is_error"] != true || !strings.Contains(generic["result"].(map[string]any)["error"].(string), "unknown action") {
		t.Fatalf("system/ping remained routable through generic registry: %#v", generic)
	}

	unauthorizedServer := &Server{Service: server.Service}
	unauthorized := callMCP(t, unauthorizedServer, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "project/workflow_policy_update", "input": map[string]any{"unknown": true}}},
	}))
	unauthorizedResult := genericStructured(t, unauthorized)
	if unauthorizedResult["is_error"] != true || !strings.Contains(unauthorizedResult["result"].(map[string]any)["error"].(string), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("generic mutation did not reuse authority enforcement: %#v", unauthorizedResult)
	}
}

func TestGenericTransportEnvelopeAndActionPathContracts(t *testing.T) {
	callSchema := genericCallOutputSchema()
	callProperties := callSchema["properties"].(map[string]any)
	if len(callProperties) != 2 {
		t.Fatalf("single-call schema has unexpected properties: %#v", callProperties)
	}
	if _, ok := callProperties["action"]; ok {
		t.Fatal("single-call schema still exposes action")
	}
	batchSchema := genericBatchOutputSchema()
	items := batchSchema["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)
	itemProperties := items["properties"].(map[string]any)
	if len(itemProperties) != 3 {
		t.Fatalf("batch item schema has unexpected properties: %#v", itemProperties)
	}
	if _, ok := itemProperties["action"]; !ok {
		t.Fatal("batch item schema lost action correlation")
	}

	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "task/read", want: true},
		{path: "task.create", want: false},
		{path: "task/read/extra", want: false},
		{path: "query/run", want: true},
	} {
		if _, _, ok := genericActionParts(test.path); ok != test.want {
			t.Fatalf("genericActionParts(%q) ok=%v, want %v", test.path, ok, test.want)
		}
	}
}

func TestGenericWorkflowPolicyMutationMatchesLegacyHandler(t *testing.T) {
	s, hubRevision := newWorkflowPolicyStatusService(t)
	current, err := s.ProjectWorkflowPolicyRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	current.Revision++
	current.UpdatedBy = "generic-equivalence-test"
	current.UpdatedAt = time.Now().UTC()
	policyJSON, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	if err := json.Unmarshal(policyJSON, &policy); err != nil {
		t.Fatal(err)
	}
	delete(policy, "project_id")
	input := map[string]any{"policy": policy, "expected_hub_revision": hubRevision}
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	sessionID := genericSessionWithRole(t, s, "example", durableSession.RolePlanner)
	legacy := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "project/workflow_policy_update", "input": input}},
	})))
	legacyPolicy := legacy["policy"].(map[string]any)
	if legacyPolicy["revision"] != float64(current.Revision) {
		t.Fatalf("legacy policy mutation did not publish expected revision: %#v", legacy)
	}

	nextRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current.Revision++
	current.UpdatedAt = time.Now().UTC()
	policyJSON, err = json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(policyJSON, &policy); err != nil {
		t.Fatal(err)
	}
	delete(policy, "project_id")
	genericInput := map[string]any{"policy": policy, "expected_hub_revision": nextRevision}
	generic := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "project/workflow_policy_update", "input": genericInput}},
	})))
	if generic["is_error"] != false {
		t.Fatalf("generic policy mutation failed: %#v", generic)
	}
	result := generic["result"].(map[string]any)
	if result["policy"].(map[string]any)["revision"] != float64(current.Revision) {
		t.Fatalf("generic policy mutation diverged from legacy handler: %#v", generic)
	}
}
