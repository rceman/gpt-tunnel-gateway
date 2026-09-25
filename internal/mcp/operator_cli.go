package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

const (
	operatorTokenHeader         = "X-GPT-Tunnel-Operator-Token"
	operatorRequestLimit        = 1 << 20
	operatorResponseLimit       = 1 << 20
	operatorAdminMintPath       = "/operator/admin/session/mint"
	operatorAdminRevokePath     = "/operator/admin/session/revoke"
	operatorFailureMessageLimit = 512
)

type operatorAdminMintRequest struct {
	Label *string `json:"label,omitempty"`
}

type operatorAdminRevokeRequest struct {
	Session string `json:"session"`
}

type operatorProjectRequest struct {
	ProjectID string `json:"project_id"`
}

type operatorAgentRegisterRequest struct {
	Root    string `json:"root"`
	AgentID string `json:"agent_id,omitempty"`
	Role    string `json:"role"`
	Relay   string `json:"relay"`
}

type operatorAgentSendRequest struct {
	ProjectID string `json:"project_id"`
	Message   string `json:"message"`
}

type operatorAgentStatusRequest struct {
	ProjectID string `json:"project_id"`
}

type operatorPlanHistoryRequest struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit"`
}

type operatorPlanSectionReadRequest struct {
	ProjectID string `json:"project_id"`
	SectionID string `json:"section_id"`
}

type operatorADRReadRequest struct {
	ProjectID string `json:"project_id"`
	Key       string `json:"key"`
}

type operatorWorkRequest struct {
	Root      string `json:"root"`
	ProjectID string `json:"project_id"`
}

type operatorVerifyRequest struct {
	Root      string   `json:"root"`
	ProjectID string   `json:"project_id,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	Packages  []string `json:"packages,omitempty"`
}

type operatorVerifyStatusRequest struct {
	OperationID string `json:"operation_id"`
}

type operatorGitProject struct {
	Root          string `json:"root"`
	Mirror        string `json:"mirror"`
	Remote        string `json:"remote"`
	DefaultBranch string `json:"default_branch"`
	ProjectCode   string `json:"project_code"`
}

func (s *Server) registerOperatorCLIRoutes(mux *http.ServeMux) {
	registerOperatorJSONRoute(mux, operatorAdminMintPath, s, func(_ context.Context, in operatorAdminMintRequest) (any, error) {
		return s.Service.AdminSessionMint(in.Label)
	})
	registerOperatorJSONRoute(mux, operatorAdminRevokePath, s, func(_ context.Context, in operatorAdminRevokeRequest) (any, error) {
		return s.Service.AdminSessionRevoke(in.Session)
	})
	registerOperatorJSONRoute(mux, "/operator/project/onboard", s, func(ctx context.Context, in service.ProjectOnboardInput) (any, error) {
		if err := validateOperatorWorkspaceRoot(in.Root); err != nil {
			return nil, err
		}
		return s.Service.ProjectOnboard(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/project/list", s, func(ctx context.Context, _ struct{}) (any, error) {
		return s.Service.ProjectList(ctx)
	})
	registerOperatorJSONRoute(mux, "/operator/project/read", s, func(ctx context.Context, in operatorProjectRequest) (any, error) {
		return s.Service.ProjectRead(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/project/update", s, func(ctx context.Context, in service.ProjectUpdateInput) (any, error) {
		return s.Service.ProjectUpdate(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/project/identifiers-read", s, func(ctx context.Context, in operatorProjectRequest) (any, error) {
		return s.Service.ProjectIdentifiersRead(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/project/identifiers-adopt", s, func(ctx context.Context, in service.ProjectIdentifiersAdoptInput) (any, error) {
		identifiers, operation, err := s.Service.ProjectIdentifiersAdopt(ctx, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"identifiers": identifiers, "operation": operation}, nil
	})
	registerOperatorJSONRoute(mux, "/operator/project/workflow-policy-read", s, func(ctx context.Context, in operatorProjectRequest) (any, error) {
		return s.Service.ProjectWorkflowPolicyRead(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/project/status", s, func(ctx context.Context, in operatorProjectRequest) (any, error) {
		return s.Service.ProjectStatus(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/project/register", s, func(ctx context.Context, in service.ProjectRegisterInput) (any, error) {
		return s.Service.ProjectRegister(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/project/git-config", s, func(_ context.Context, in operatorProjectRequest) (any, error) {
		project, err := s.Service.EffectiveProjectConfig(in.ProjectID)
		if err != nil {
			return nil, err
		}
		return operatorGitProject{
			Root:          project.Root,
			Mirror:        project.Mirror,
			Remote:        project.Remote,
			DefaultBranch: project.DefaultBranch,
			ProjectCode:   project.ProjectCode,
		}, nil
	})
	registerOperatorJSONRoute(mux, "/operator/agent/register", s, func(ctx context.Context, in operatorAgentRegisterRequest) (any, error) {
		if err := validateOperatorWorkspaceRoot(in.Root); err != nil {
			return nil, err
		}
		projectID, err := s.Service.ProjectIDForRoot(ctx, in.Root)
		if err != nil {
			return nil, err
		}
		return s.Service.AgentBootstrap(ctx, service.AgentBootstrapInput{ProjectID: projectID, AgentID: in.AgentID, Role: in.Role, Relay: in.Relay})
	})
	registerOperatorJSONRoute(mux, "/operator/agent/send", s, func(ctx context.Context, in operatorAgentSendRequest) (any, error) {
		return s.Service.AgentSend(ctx, in.ProjectID, in.Message)
	})
	registerOperatorJSONRoute(mux, "/operator/agent/status", s, func(ctx context.Context, in operatorAgentStatusRequest) (any, error) {
		return s.Service.AgentStatus(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/journal/migrate", s, func(ctx context.Context, in service.JournalMigrateInput) (any, error) {
		return s.Service.JournalMigrate(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/read", s, func(ctx context.Context, in operatorProjectRequest) (any, error) {
		return s.Service.PlanRead(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/history", s, func(ctx context.Context, in operatorPlanHistoryRequest) (any, error) {
		return s.Service.PlanHistory(ctx, in.ProjectID, in.Limit)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/cutover", s, func(ctx context.Context, in service.PlanCutoverInput) (any, error) {
		return s.Service.PlanCutover(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/update", s, func(ctx context.Context, in service.PlanUpdateInput) (any, error) {
		return s.Service.PlanUpdate(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/section-read", s, func(ctx context.Context, in operatorPlanSectionReadRequest) (any, error) {
		return s.Service.PlanSectionRead(ctx, in.ProjectID, in.SectionID)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/section-create", s, func(ctx context.Context, in service.PlanSectionCreateInput) (any, error) {
		return s.Service.PlanSectionCreate(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/section-update", s, func(ctx context.Context, in service.PlanSectionUpdateInput) (any, error) {
		return s.Service.PlanSectionUpdate(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/plan/render", s, func(ctx context.Context, in operatorProjectRequest) (any, error) {
		return s.Service.PlanRender(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/adr/list", s, func(ctx context.Context, in operatorProjectRequest) (any, error) {
		return s.Service.ADRList(ctx, in.ProjectID)
	})
	registerOperatorJSONRoute(mux, "/operator/adr/read", s, func(ctx context.Context, in operatorADRReadRequest) (any, error) {
		return s.Service.ADRRead(ctx, in.ProjectID, in.Key)
	})
	registerOperatorJSONRoute(mux, "/operator/adr/create", s, func(ctx context.Context, in service.ADRCreateInput) (any, error) {
		return s.Service.ADRCreate(ctx, in)
	})
	registerOperatorJSONRoute(mux, "/operator/work/checkpoint", s, func(ctx context.Context, in operatorWorkRequest) (any, error) {
		if err := validateOperatorWorkspaceRoot(in.Root); err != nil {
			return nil, err
		}
		return s.Service.WorkCheckpoint(ctx, service.WorkProgressInput{Root: in.Root, ProjectID: in.ProjectID})
	})
	registerOperatorJSONRoute(mux, "/operator/work/status", s, func(ctx context.Context, in operatorWorkRequest) (any, error) {
		if err := validateOperatorWorkspaceRoot(in.Root); err != nil {
			return nil, err
		}
		return s.Service.WorkCheckpointStatus(ctx, service.WorkCheckpointInput{Root: in.Root, ProjectID: in.ProjectID})
	})
	registerOperatorJSONRoute(mux, "/operator/verify", s, func(ctx context.Context, in operatorVerifyRequest) (any, error) {
		if err := validateOperatorWorkspaceRoot(in.Root); err != nil {
			return nil, err
		}
		return s.Service.Verify(ctx, service.VerifyInput{Root: in.Root, ProjectID: in.ProjectID, Scope: in.Scope, Packages: in.Packages})
	})
	registerOperatorJSONRoute(mux, "/operator/verify/status", s, func(ctx context.Context, in operatorVerifyStatusRequest) (any, error) {
		return s.Service.VerifyStatus(ctx, in.OperationID)
	})
}

func validateOperatorWorkspaceRoot(root string) error {
	if root == "" || strings.TrimSpace(root) != root || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsAny(root, "\x00\r\n") {
		return errors.New("operator workspace root must be absolute and canonical")
	}
	return nil
}

func registerOperatorJSONRoute[Input any](mux *http.ServeMux, path string, s *Server, call func(context.Context, Input) (any, error)) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeOperatorFailure(w, http.StatusMethodNotAllowed, "INVALID_REQUEST", "operator request accepts POST only")
			return
		}
		credential := r.Header.Get(operatorTokenHeader)
		if credential == "" {
			writeOperatorFailure(w, http.StatusUnauthorized, "OPERATOR_CREDENTIAL_REQUIRED", "the local operator credential is required")
			return
		}
		if s == nil || s.Service == nil || !s.Service.ValidOperatorToken(credential) {
			writeOperatorFailure(w, http.StatusForbidden, "OPERATOR_CREDENTIAL_UNAUTHORIZED", "the local operator credential is not authorized")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, operatorRequestLimit)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var input Input
		if err := decoder.Decode(&input); err != nil {
			writeOperatorFailure(w, http.StatusBadRequest, "INVALID_REQUEST", "operator request is invalid")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeOperatorFailure(w, http.StatusBadRequest, "INVALID_REQUEST", "operator request must contain one JSON object")
			return
		}
		result, err := call(r.Context(), input)
		if err != nil {
			writeOperatorFailure(w, http.StatusConflict, "OPERATOR_OPERATION_FAILED", operatorOperationFailureMessage(err, credential))
			return
		}
		payload, err := json.Marshal(map[string]any{"ok": true, "result": result})
		if err != nil || len(payload) > operatorResponseLimit {
			writeOperatorFailure(w, http.StatusInternalServerError, "OPERATOR_RESPONSE_INVALID", "operator response exceeds bounds")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
}

func operatorOperationFailureMessage(err error, credential string) string {
	message := err.Error()
	if credential != "" {
		message = strings.ReplaceAll(message, credential, "[redacted]")
	}
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return "the requested operator operation failed"
	}
	if len(message) <= operatorFailureMessageLimit {
		return message
	}
	var bounded strings.Builder
	for _, r := range message {
		if bounded.Len()+utf8.RuneLen(r) > operatorFailureMessageLimit-len("...") {
			break
		}
		bounded.WriteRune(r)
	}
	bounded.WriteString("...")
	return bounded.String()
}

func writeOperatorFailure(w http.ResponseWriter, status int, code, message string) {
	payload, err := json.Marshal(map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
	if err != nil {
		http.Error(w, "operator request failed", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
