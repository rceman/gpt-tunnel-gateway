package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

const operatorProjectTokenPath = "/operator/project/token"

type operatorProjectTokenRequest struct {
	Root    string            `json:"root"`
	Remote  string            `json:"remote,omitempty"`
	Remotes map[string]string `json:"remotes,omitempty"`
}

func (s *Server) registerOperatorProjectTokenRoute(mux *http.ServeMux) {
	mux.HandleFunc(operatorProjectTokenPath, s.handleOperatorProjectToken)
}

func (s *Server) handleOperatorProjectToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOperatorProjectTokenFailure(w, http.StatusMethodNotAllowed, "INVALID_REQUEST", "project token accepts POST only")
		return
	}
	adminSession := strings.TrimSpace(r.Header.Get("X-GPT-Tunnel-Admin-Session"))
	if adminSession == "" {
		writeOperatorProjectTokenFailure(w, http.StatusUnauthorized, "ADMIN_SESSION_REQUIRED", "an active Admin Session is required")
		return
	}
	if _, err := s.Service.AdminSession(r.Context(), adminSession); err != nil {
		writeOperatorProjectTokenFailure(w, http.StatusForbidden, "ADMIN_SESSION_UNAUTHORIZED", "the Admin Session is not authorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var request operatorProjectTokenRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeOperatorProjectTokenFailure(w, http.StatusBadRequest, "INVALID_REQUEST", "project token request is invalid")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeOperatorProjectTokenFailure(w, http.StatusBadRequest, "INVALID_REQUEST", "project token request must contain one JSON object")
		return
	}
	result, err := s.Service.ProjectToken(r.Context(), service.ProjectTokenInput{Root: request.Root, Remote: request.Remote, Remotes: request.Remotes})
	if err != nil {
		writeOperatorProjectTokenFailure(w, http.StatusConflict, "PROJECT_TOKEN_UNAVAILABLE", err.Error())
		return
	}
	writeOperatorProjectTokenJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

func writeOperatorProjectTokenJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOperatorProjectTokenFailure(w http.ResponseWriter, status int, code, message string) {
	writeOperatorProjectTokenJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": fmt.Sprint(message)},
	})
}
