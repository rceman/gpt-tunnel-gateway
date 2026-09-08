package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK514AgentInventoryKeepsDisabledCodingAndHidesRetiredWatcher(t *testing.T) {
	server := newSessionTestServer(t)
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedMCPTestCodingAgent(t, server.Service, revision)
	now := time.Now().UTC()
	for _, agent := range []model.Agent{
		{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "coding-disabled", Role: model.AgentRoleCoding, Enabled: false, RecommendedReasoning: model.ReasoningHigh, CreatedAt: now, UpdatedAt: now},
		{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "retired-watcher", Role: "watcher", Enabled: false, CreatedAt: now, UpdatedAt: now},
	} {
		payload, err := json.Marshal(agent)
		if err != nil {
			t.Fatal(err)
		}
		if err := server.Service.Durability.UpsertLocalAgent(context.Background(), sqlitestore.LocalAgent{
			ProjectID: "example", AgentID: agent.AgentID, Payload: payload, UpdatedAt: now.Format(time.RFC3339Nano),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	agents, err := server.Service.AgentList(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 || agents[0].AgentID != "coding-disabled" || agents[1].AgentID != "coding-example" {
		t.Fatalf("local coding inventory=%#v", agents)
	}

	sessionID := genericSession(t, server.Service, "example")
	response := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 514, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "agent/list", "input": map[string]any{},
		}},
	})))
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("public agent/list result=%#v", response)
	}
	publicAgents, ok := result["agents"].([]any)
	if !ok || len(publicAgents) != 1 || publicAgents[0].(map[string]any)["key"] != "coding-example" {
		t.Fatalf("public agent/list projection=%#v", response)
	}
}

func schemaProperties(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	return properties
}

func TestCanonicalAgentActionsHaveExactADR85Surface(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "home_pc", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	want := []string{"agent/await", "agent/guide", "agent/interrupt", "agent/list", "agent/prompt", "agent/status", "agent/tail"}
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
	for _, name := range []string{"agent_send", "agent_tail", "agent_status"} {
		if _, ok := server.tools()[name]; ok {
			t.Fatalf("legacy Agent route remains registered: %s", name)
		}
		if _, ok := toolOutputSchemas[name]; ok {
			t.Fatalf("legacy Agent output schema remains registered: %s", name)
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
	assertFields("agent/tail", []string{"session", "lines"}, []string{})
	assertFields("agent/prompt", []string{"agent", "message"}, []string{"message"})
	assertFields("agent/interrupt", []string{"agent", "message"}, []string{})

	for _, path := range []string{"agent/prompt", "agent/interrupt"} {
		message := schemaProperties(entries[path].InputSchema)["message"].(map[string]any)
		if message["maxLength"] != airelay.MaxPromptBytes {
			t.Fatalf("%s message maxLength=%v, want=%d", path, message["maxLength"], airelay.MaxPromptBytes)
		}
	}
	seconds := schemaProperties(entries["agent/await"].InputSchema)["seconds"].(map[string]any)
	if seconds["minimum"] != 1 || seconds["maximum"] != 600 || seconds["default"] != canonicalAgentAwaitDefaultSeconds {
		t.Fatalf("await seconds contract=%#v", seconds)
	}
	for _, path := range []string{"agent/list", "agent/status", "agent/await", "agent/tail", "agent/prompt", "agent/interrupt"} {
		if entries[path].OutputSchema["additionalProperties"] != false {
			t.Fatalf("%s output is not closed", path)
		}
	}
}

func newInstrumentedAwaitFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, s, revision)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "live-calls")
	command := filepath.Join(dir, "airelay")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s %%s\n' "$(date +%%s%%3N)" "$*" >> %q
case "$1" in
session-status) if [ "$3" = --json ]; then printf '{"sessionKey":"%%s","profile":"coding","controllerReachable":true,"state":"idle"}' "$2"; else printf 'Controller: reachable\nState: idle\n'; fi ;;
tail) printf 'new\n' ;;
*) exit 99 ;;
esac
`, logPath)
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Config.AirelayCommand = command
	s.Airelay.Command = command
	s.Config.AgentBindings = map[string]config.AgentBinding{
		config.ProjectAgentBindingKey("example", "coding-example"): {SessionKey: "example_master", Profile: "coding"},
	}
	server := &Server{Service: s}
	sessionID := genericSession(t, s, "example")
	return server, sessionID, logPath
}

func TestCanonicalAgentAwaitDefersLiveProbeAndPreservesTail(t *testing.T) {
	server, sessionID, logPath := newInstrumentedAwaitFixture(t)
	started := time.Now()
	resultCh := make(chan struct {
		value any
		err   error
	}, 1)
	go func() {
		value, err := server.canonicalAgentAwaitAction(
			service.WithAgentSessionID(context.Background(), sessionID),
			mustJSON(t, map[string]any{"seconds": 2, "agent": "coding-example"}),
		)
		resultCh <- struct {
			value any
			err   error
		}{value, err}
	}()
	time.Sleep(250 * time.Millisecond)
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("live Airelay call occurred before defer boundary: %q", data)
	}
	result := <-resultCh
	if result.err != nil {
		t.Fatal(result.err)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed >= 3*time.Second {
		t.Fatalf("await duration=%s, want approximately two seconds and below three seconds", elapsed)
	}
	if data, err := os.ReadFile(logPath); err != nil || len(data) == 0 {
		t.Fatalf("final live probe was not recorded: %q, %v", data, err)
	}
	value := result.value.(map[string]any)
	if value["agent"] != "coding-example" || value["status"] != "idle" {
		t.Fatalf("await returned unexpected status: %#v", value)
	}
	if tail, ok := value["tail"].([]string); !ok || !reflect.DeepEqual(tail, []string{"new"}) {
		t.Fatalf("await tail projection=%#v", value["tail"])
	}
}

func TestCanonicalAgentAwaitCancellationBeforeProbeIsPrompt(t *testing.T) {
	server, sessionID, logPath := newInstrumentedAwaitFixture(t)
	ctx, cancel := context.WithCancel(service.WithAgentSessionID(context.Background(), sessionID))
	defer cancel()
	resultCh := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := server.canonicalAgentAwaitAction(ctx, mustJSON(t, map[string]any{"seconds": 2, "agent": "coding-example"}))
		resultCh <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("await cancellation error=%v", err)
		}
		if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
			t.Fatalf("await cancellation took %s", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("await did not return promptly after cancellation")
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("cancellation triggered live Airelay call: %q", data)
	}
}

func TestCanonicalAgentAwaitOneSecondEntersFinalProbeImmediately(t *testing.T) {
	server, sessionID, logPath := newInstrumentedAwaitFixture(t)
	started := time.Now()
	value, err := server.canonicalAgentAwaitAction(
		service.WithAgentSessionID(context.Background(), sessionID),
		mustJSON(t, map[string]any{"seconds": 1, "agent": "coding-example"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 1500*time.Millisecond {
		t.Fatalf("one-second await exceeded bounded window: %s", elapsed)
	}
	if data, err := os.ReadFile(logPath); err != nil || len(data) == 0 {
		t.Fatalf("one-second await did not perform final probe: %q, %v", data, err)
	}
	if result := value.(map[string]any); result["status"] != "idle" {
		t.Fatalf("one-second await result=%#v", result)
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
