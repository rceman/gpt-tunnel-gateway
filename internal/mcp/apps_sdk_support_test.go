package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func callMCP(t *testing.T, srv *Server, body []byte) map[string]any {
	t.Helper()
	var original map[string]any
	_ = json.Unmarshal(body, &original)
	originalParams, _ := original["params"].(map[string]any)
	originalName, _ := originalParams["name"].(string)
	body = normalizeLegacyTransportTestRequest(t, body)
	response := callMCPRaw(t, srv, body)
	if originalName != "" && originalName != "call" && originalName != "batch" && originalName != "schema" {
		if result, ok := response["result"].(map[string]any); ok {
			if structured, ok := result["structuredContent"].(map[string]any); ok && structured["is_error"] == false {
				if value, ok := structured["result"]; ok {
					result["structuredContent"] = value
				}
			}
		}
	}
	return response
}

func callMCPRaw(t *testing.T, srv *Server, body []byte) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(body))
	req.Host = "127.0.0.1:1"
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	return response
}

// normalizeLegacyTransportTestRequest keeps pre-cutover behavior tests focused
// on their service semantics while routing them through the canonical call
// envelope. The production router intentionally has no such compatibility.
func normalizeLegacyTransportTestRequest(t *testing.T, body []byte) []byte {
	t.Helper()
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return body
	}
	params, ok := request["params"].(map[string]any)
	if !ok {
		return body
	}
	name, _ := params["name"].(string)
	arguments, _ := params["arguments"].(map[string]any)
	if arguments == nil {
		return body
	}
	if sessionID, ok := arguments["session_id"]; ok {
		arguments["session"] = sessionID
		delete(arguments, "session_id")
	}
	if name == "call" || name == "batch" {
		if name == "batch" {
			if _, ok := arguments["session"]; !ok {
				if sessionID, ok := arguments["session_id"]; ok {
					arguments["session"] = sessionID
					delete(arguments, "session_id")
				}
			}
		}
		return mustJSON(t, request)
	}
	var action string
	switch name {
	case "status", "system_ping":
		action = "gateway/status"
	case "rules":
		action = "workflow/rules"
	case "project":
		name, _ = arguments["action"].(string)
		action = "project/" + name
		if input, ok := arguments["input"].(map[string]any); ok {
			arguments = input
		}
	case "session":
		name, _ = arguments["action"].(string)
		action = "session/" + name
		delete(arguments, "action")
		if sessionType, ok := arguments["session_type"].(string); ok && sessionType == "chatgpt" {
			delete(arguments, "session_type")
		}
		if ref, ok := arguments["session_ref"]; ok {
			arguments["ref"] = ref
			delete(arguments, "session_ref")
		}
	default:
		return mustJSON(t, request)
	}
	newArguments := map[string]any{"action": action, "input": arguments}
	if session, ok := arguments["session"]; ok {
		newArguments["session"] = session
		delete(arguments, "session")
	}
	params["name"] = "call"
	params["arguments"] = newArguments
	return mustJSON(t, request)
}

func adoptTestWorkflowPolicy(t *testing.T, s *service.Service, projectID, revision string) string {
	t.Helper()
	now := time.Now().UTC()
	policy := model.ProjectWorkflowPolicy{SchemaVersion: model.SchemaVersion, ProjectID: projectID, Revision: 1, WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main", Agent: model.WorkflowPolicyAgent{WaitForCI: false}, CI: model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeObserve, Release: model.WorkflowCIModeObserve}, UpdatedBy: "test", UpdatedAt: now}
	_, result, err := s.ProjectWorkflowPolicyAdopt(service.WithPlannerWorkflowPolicyAuthority(context.Background()), service.ProjectWorkflowPolicyInput{Policy: policy, WriteOptions: service.WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	return result.Hub.After
}
