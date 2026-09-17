package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const (
	agentCLISubmitCodePath   = "/agent-cli/task/submit-code"
	agentCLISubmitTestsPath  = "/agent-cli/task/submit-tests"
	agentCLISubmitRebasePath = "/agent-cli/task/submit-rebase"
	maxAgentCLISubmitBody    = 4 << 10
)

var agentCLISubmitActions = map[string]string{
	agentCLISubmitCodePath:   "task/submit-code",
	agentCLISubmitTestsPath:  "task/submit-tests",
	agentCLISubmitRebasePath: "task/submit-rebase",
}

type agentCLISubmitRequest struct {
	Runtime string `json:"runtime"`
}

func (s *Server) registerAgentCLISubmitRoutes(mux *http.ServeMux) {
	for path, action := range agentCLISubmitActions {
		submitAction := action
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			s.handleAgentCLISubmit(w, r, submitAction)
		})
	}
}

func (s *Server) handleAgentCLISubmit(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		writeAgentCLISubmitFailure(w, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Agent CLI Task submission accepts POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentCLISubmitBody)
	request, err := decodeAgentCLISubmitRequest(r)
	if err != nil {
		writeAgentCLISubmitFailure(w, http.StatusBadRequest, "INVALID_REQUEST", `Agent CLI Task submission body must be exactly {"runtime":"<airelay runtime>"}`)
		return
	}
	if request.Runtime == "" || strings.TrimSpace(request.Runtime) != request.Runtime {
		writeAgentCLISubmitFailure(w, http.StatusBadRequest, "INVALID_REQUEST", "Agent CLI Task submission requires the calling Airelay runtime")
		return
	}
	resolved, err := s.Service.ResolveRuntimeRoleSession(r.Context(), request.Runtime, durableSession.RoleWorker)
	if err != nil {
		writeAgentCLISubmitFailure(w, http.StatusForbidden, "RUNTIME_IDENTITY_UNAVAILABLE", err.Error())
		return
	}
	if err := validateAgentCLISubmitWorkerAuthority(resolved); err != nil {
		writeAgentCLISubmitFailure(w, http.StatusForbidden, "RUNTIME_ROLE_UNAUTHORIZED", err.Error())
		return
	}
	value, err := s.dispatchAgentCLISubmit(r.Context(), action, resolved.Session.ID)
	if err != nil {
		writeAgentCLISubmitFailure(w, http.StatusInternalServerError, "CALL_FAILED", err.Error())
		return
	}
	writeAgentCLISubmitJSON(w, http.StatusOK, value)
}

func decodeAgentCLISubmitRequest(r *http.Request) (agentCLISubmitRequest, error) {
	var request agentCLISubmitRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return agentCLISubmitRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return agentCLISubmitRequest{}, fmt.Errorf("body must contain exactly one JSON object")
	}
	return request, nil
}

func validateAgentCLISubmitWorkerAuthority(resolved service.RuntimeRoleSession) error {
	if resolved.Agent.WorkflowRole != durableSession.RoleWorker {
		return fmt.Errorf("RUNTIME_ROLE_UNAUTHORIZED: resolved Agent is not the portable Worker identity")
	}
	if !resolved.Agent.Enabled || resolved.Agent.ProjectID != resolved.ProjectID {
		return fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: resolved Agent is not an enabled Agent of the resolved project")
	}
	if resolved.Session.Status != durableSession.StatusActive || resolved.Session.ProjectID != resolved.ProjectID || resolved.Session.Role != durableSession.RoleWorker {
		return fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: resolved Session is not an active project-bound Worker Session")
	}
	if resolved.Binding.SessionKey == "" || resolved.Session.SessionRef == nil || *resolved.Session.SessionRef != resolved.Binding.SessionKey {
		return fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: resolved Worker Session is not bound to the calling runtime")
	}
	return nil
}

func (s *Server) dispatchAgentCLISubmit(ctx context.Context, action, sessionID string) (any, error) {
	call, ok := s.publicTools()["call"]
	if !ok || call.Execute == nil {
		return nil, fmt.Errorf("canonical Gateway action transport is unavailable")
	}
	raw, err := json.Marshal(map[string]any{"session": sessionID, "action": action, "input": map[string]any{}})
	if err != nil {
		return nil, err
	}
	return call.Execute(authority.Attach(ctx, s.AuthorityContext), raw)
}

func writeAgentCLISubmitJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAgentCLISubmitFailure(w http.ResponseWriter, status int, code, message string) {
	writeAgentCLISubmitJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": message},
	})
}
