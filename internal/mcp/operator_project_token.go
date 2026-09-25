package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

const operatorProjectTokenPath = "/operator/project/token"

func (s *Server) registerOperatorProjectTokenRoute(mux *http.ServeMux) {
	mux.HandleFunc(operatorProjectTokenPath, s.handleOperatorProjectToken)
}

func (s *Server) handleOperatorProjectToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOperatorProjectTokenFailure(w, http.StatusMethodNotAllowed, "INVALID_REQUEST", "project token accepts POST only")
		return
	}
	credential := r.Header.Get(operatorTokenHeader)
	if credential == "" {
		writeOperatorProjectTokenFailure(w, http.StatusUnauthorized, "OPERATOR_CREDENTIAL_REQUIRED", "the local operator credential is required")
		return
	}
	if s == nil || s.Service == nil || !s.Service.ValidOperatorToken(credential) {
		writeOperatorProjectTokenFailure(w, http.StatusForbidden, "OPERATOR_CREDENTIAL_UNAUTHORIZED", "the local operator credential is not authorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, operatorRequestLimit)
	var request service.ProjectTokenInput
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
	result, err := s.Service.ProjectToken(r.Context(), request)
	if err != nil {
		writeOperatorProjectTokenFailure(w, http.StatusConflict, "PROJECT_TOKEN_UNAVAILABLE", err.Error())
		return
	}
	writeOperatorProjectTokenJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

func writeOperatorProjectTokenJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOperatorProjectTokenFailure(w http.ResponseWriter, status int, code, message string) {
	writeOperatorProjectTokenJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": fmt.Sprint(message)},
	})
}
