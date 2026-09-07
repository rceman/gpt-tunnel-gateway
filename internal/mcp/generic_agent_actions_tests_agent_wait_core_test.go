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

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestCanonicalAgentMessageValidationUsesUTF8ByteBound(t *testing.T) {
	if err := validateCanonicalAgentMessage("ok"); err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalAgentMessage(strings.Repeat("a", 241)); err != nil {
		t.Fatalf("message over old bound rejected: %v", err)
	}
	if err := validateCanonicalAgentMessage(strings.Repeat("a", airelay.MaxPromptBytes)); err != nil {
		t.Fatalf("message at ASCII byte bound rejected: %v", err)
	}
	if err := validateCanonicalAgentMessage(strings.Repeat("é", airelay.MaxPromptBytes/2)); err != nil {
		t.Fatalf("message at UTF-8 byte bound rejected: %v", err)
	}
	if err := validateCanonicalAgentMessage(string(make([]byte, airelay.MaxPromptBytes+1))); err == nil {
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

func TestCanonicalAgentAwaitPreservesTailProjection(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, s, revision)
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
session-status) if [ "$3" = --json ]; then printf '{"sessionKey":"%s","profile":"coding","controllerReachable":true,"state":"idle"}' "$2"; else printf 'Controller: reachable\nState: idle\n'; fi ;;
tail) printf 'new\n' ;;
*) exit 99 ;;
esac
`
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

func TestCanonicalAgentAwaitPreservesTailTruncatedProjection(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, s, revision)
	dir := t.TempDir()
	marker := filepath.Join(dir, "tail-called")
	command := filepath.Join(dir, "airelay")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
tail)
if [ -f %q ]; then printf 'fresh\n'; else
i=1
while [ "$i" -le 30 ]; do printf 'old%%s\n' "$i"; i=$((i+1)); done
touch %q
fi ;;
*) exit 99 ;;
esac
`, marker, marker)
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Config.AirelayCommand = command
	s.Airelay.Command = command
	sessionID := genericSession(t, s, "example")
	if _, err := s.AgentTailPage(context.Background(), "example", service.AgentTailInput{
		Lines: 30, SessionID: sessionID, SessionKey: "example_master",
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{Service: s}
	value, err := server.canonicalAgentAwaitResult(service.WithAgentSessionID(context.Background(), sessionID), "example", canonicalAgentTarget{
		Agent:    model.Agent{AgentID: "coding-example"},
		Resolved: service.ResolvedAgent{SessionKey: "example_master"},
	}, map[string]any{"agent": "coding-example", "status": "idle"})
	if err != nil {
		t.Fatal(err)
	}
	if value["tail_truncated"] != true {
		t.Fatalf("await omitted tail_truncated projection: %#v", value)
	}
}

func TestCanonicalAgentPublicMCPContractE2E(t *testing.T) {
	s, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, s, revision)
	server := &Server{
		Service:          s,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	sessionID := genericSession(t, s, "example")

	schema := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": "agent"}},
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
