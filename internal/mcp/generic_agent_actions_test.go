package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func schemaProperties(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	return properties
}

func TestCanonicalAgentActionsHaveExactADR85Surface(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	want := []string{"agent/await", "agent/interrupt", "agent/list", "agent/prompt", "agent/status", "agent/tail"}
	got := make([]string, 0)
	for path := range entries {
		if len(path) >= len("agent/") && path[:len("agent/")] == "agent/" {
			got = append(got, path)
		}
	}
	sortStrings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Agent action inventory=%v, want=%v", got, want)
	}
	for _, path := range want {
		entry := entries[path]
		if !entry.SessionBound || !entry.SessionRequired {
			t.Fatalf("%s is not session-bound and required: %#v", path, entry)
		}
		if _, ok := schemaProperties(entry.InputSchema)["project_id"]; ok {
			t.Fatalf("%s exposes project_id", path)
		}
	}
	for _, path := range []string{"agent/read", "agent/recover", "agent/update", "agent/disable"} {
		if _, ok := entries[path]; ok {
			t.Fatalf("retired Agent action remains registered: %s", path)
		}
	}
}

func TestCanonicalAgentSchemasAreClosedAndBounded(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	assertFields := func(path string, fields, required []string) {
		schema := entries[path].InputSchema
		if schema["additionalProperties"] != false {
			t.Fatalf("%s input is not closed: %#v", path, schema)
		}
		properties := schemaProperties(schema)
		got := make([]string, 0, len(properties))
		for field := range properties {
			got = append(got, field)
		}
		sortStrings(got)
		want := append([]string{}, fields...)
		sortStrings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s input fields=%v, want=%v", path, got, want)
		}
		gotRequired := stringList(schema["required"])
		if len(gotRequired) != len(required) {
			t.Fatalf("%s required=%v, want=%v", path, gotRequired, required)
		}
		for i := range required {
			if gotRequired[i] != required[i] {
				t.Fatalf("%s required=%v, want=%v", path, gotRequired, required)
			}
		}
	}
	assertFields("agent/list", []string{}, []string{})
	assertFields("agent/status", []string{"agent"}, []string{})
	assertFields("agent/await", []string{"agent", "seconds"}, []string{})
	assertFields("agent/tail", []string{"agent", "lines"}, []string{})
	assertFields("agent/prompt", []string{"agent", "message"}, []string{"message"})
	assertFields("agent/interrupt", []string{"agent", "message"}, []string{})

	message := schemaProperties(entries["agent/prompt"].InputSchema)["message"].(map[string]any)
	if message["maxLength"] != canonicalAgentMessageMaxBytes {
		t.Fatalf("prompt message maxLength=%v, want=%d", message["maxLength"], canonicalAgentMessageMaxBytes)
	}
	seconds := schemaProperties(entries["agent/await"].InputSchema)["seconds"].(map[string]any)
	if seconds["minimum"] != 1 || seconds["maximum"] != 600 || seconds["default"] != 60 {
		t.Fatalf("await seconds contract=%#v", seconds)
	}
	for _, path := range []string{"agent/list", "agent/status", "agent/await", "agent/tail", "agent/prompt", "agent/interrupt"} {
		if entries[path].OutputSchema["additionalProperties"] != false {
			t.Fatalf("%s output is not closed", path)
		}
	}
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func TestCanonicalAgentMessageValidationUsesUTF8ByteBound(t *testing.T) {
	if err := validateCanonicalAgentMessage("ok"); err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalAgentMessage(string(make([]byte, canonicalAgentMessageMaxBytes+1))); err == nil {
		t.Fatal("oversized message accepted")
	}
	if err := validateCanonicalAgentMessage("\x00"); err == nil {
		t.Fatal("NUL message accepted")
	}
	if err := validateOptionalCanonicalAgentMessage(""); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalAgentBusyUsesAirelaySessionState(t *testing.T) {
	availability := model.AgentAvailabilityStatus{
		Enabled:      true,
		State:        "usable",
		AttemptState: model.TrainV2AttemptRunning,
		SessionState: "idle",
	}
	if got := canonicalAgentAvailabilityState(availability); got != "idle" {
		t.Fatalf("busy Attempt incorrectly made Agent busy: got %q", got)
	}
	availability.AttemptState = ""
	availability.SessionState = "running"
	if got := canonicalAgentAvailabilityState(availability); got != "busy" {
		t.Fatalf("running Airelay session was not busy: got %q", got)
	}
}

func TestCanonicalAgentAwaitTimeoutReturnsIncrementalTail(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, s, revision)
	dir := t.TempDir()
	counter := filepath.Join(dir, "tail-called")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) if [ -f %s ]; then printf 'old\\nnew\\n'; else printf 'old\\n'; touch %s; fi ;;\n*) exit 0 ;;\nesac\n", counter, counter)
	command := filepath.Join(dir, "airelay")
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Config.AirelayCommand = command
	s.Airelay.Command = command
	server := &Server{Service: s}
	sessionID := genericSession(t, s, "example")
	value, err := server.canonicalAgentAwaitAction(
		service.WithAgentSessionID(context.Background(), sessionID),
		mustJSON(t, map[string]any{"seconds": 1, "agent": "coding-example"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if result["agent"] != "coding-example" || result["status"] != "idle" {
		t.Fatalf("await returned unexpected status: %#v", result)
	}
	tail, ok := result["tail"].([]string)
	if !ok || !reflect.DeepEqual(tail, []string{"new"}) {
		t.Fatalf("await did not return incremental tail: %#v", result)
	}
}

func TestCanonicalAgentPublicMCPContractE2E(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, s, revision)
	server := &Server{Service: s, AuthorityContext: authority.WithDelivery(context.Background())}
	sessionID := genericSession(t, s, "example")

	schema := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "agent"}},
	})))
	actions := schema["actions"].([]any)
	paths := make([]string, 0, len(actions))
	for _, rawAction := range actions {
		action := rawAction.(map[string]any)
		paths = append(paths, action["path"].(string))
	}
	sortStrings(paths)
	if !reflect.DeepEqual(paths, []string{"agent/await", "agent/interrupt", "agent/list", "agent/prompt", "agent/status", "agent/tail"}) {
		t.Fatalf("public Agent schema paths=%v", paths)
	}

	call := func(id int, action string, input map[string]any) map[string]any {
		t.Helper()
		structured := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session": sessionID, "action": action, "input": input,
			}},
		})))
		result, ok := structured["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s omitted result: %#v", action, structured)
		}
		return map[string]any{"envelope": structured, "result": result}
	}

	list := call(2, "agent/list", map[string]any{})
	if list["envelope"].(map[string]any)["is_error"] != false || len(list["result"].(map[string]any)["agents"].([]any)) != 1 {
		t.Fatalf("agent/list failed: %#v", list)
	}
	status := call(3, "agent/status", map[string]any{"agent": "coding-example"})
	statusResult := status["result"].(map[string]any)
	if status["envelope"].(map[string]any)["is_error"] != false || statusResult["agent"] != "coding-example" {
		t.Fatalf("agent/status failed: %#v", status)
	}
	tail := call(4, "agent/tail", map[string]any{"agent": "coding-example", "lines": 1})
	if tail["envelope"].(map[string]any)["is_error"] != false || tail["result"].(map[string]any)["agent"] != "coding-example" {
		t.Fatalf("agent/tail failed: %#v", tail)
	}
	projectIDTail := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 40, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "agent/tail", "input": map[string]any{"project_id": "example"},
		}},
	}))
	if genericStructured(t, projectIDTail)["is_error"] != true {
		t.Fatalf("agent/tail accepted caller project_id: %#v", projectIDTail)
	}
	prompt := call(5, "agent/prompt", map[string]any{"agent": "coding-example", "message": "contract"})
	promptResult := prompt["result"].(map[string]any)
	if prompt["envelope"].(map[string]any)["is_error"] != false || promptResult["operation"] == "" || promptResult["status"] != "accepted" {
		t.Fatalf("agent/prompt failed: %#v", prompt)
	}
	awaited := call(6, "agent/await", map[string]any{"agent": "coding-example", "seconds": 1})
	if awaited["envelope"].(map[string]any)["is_error"] != false || awaited["result"].(map[string]any)["agent"] != "coding-example" {
		t.Fatalf("agent/await failed: %#v", awaited)
	}
	interrupt := call(7, "agent/interrupt", map[string]any{"agent": "coding-example"})
	if interrupt["envelope"].(map[string]any)["is_error"] != false {
		t.Fatalf("agent/interrupt did not enqueue a current-Agent operation: %#v", interrupt)
	}
	interruptResult := interrupt["result"].(map[string]any)
	if interruptResult["status"] != "accepted" || interruptResult["operation"] == "" {
		t.Fatalf("agent/interrupt did not return an accepted receipt: %#v", interrupt)
	}
	interruptOperation := interruptResult["operation"].(string)
	deadline := time.Now().Add(10 * time.Second)
	terminal := false
	for time.Now().Before(deadline) {
		value, statusErr := s.AgentIPCOperationStatus(context.Background(), interruptOperation, "agent-interrupt")
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		statusReceipt := value.(service.AgentInterruptReceipt)
		if statusReceipt.Status == "completed" || statusReceipt.Status == "failed" {
			terminal = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !terminal {
		t.Fatal("agent/interrupt receipt did not become terminal")
	}

	for _, action := range []string{"agent/read", "agent/recover", "agent/update", "agent/disable"} {
		response := callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 8, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session": sessionID, "action": action, "input": map[string]any{},
			}},
		}))
		if !strings.Contains(string(mustJSON(t, response)), "unknown action") {
			t.Fatalf("retired action %s was not rejected: %#v", action, response)
		}
	}
}
