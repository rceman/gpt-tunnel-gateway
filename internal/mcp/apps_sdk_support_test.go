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
