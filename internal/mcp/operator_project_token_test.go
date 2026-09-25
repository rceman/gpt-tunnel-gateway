package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestOperatorProjectTokenRouteUsesOwnerPrivateTokenAndExplicitProjectCode(t *testing.T) {
	_, root, _ := testutil.RepoWithBareRemote(t)
	cfg := config.Config{
		GatewayID:    "HOM",
		MaxReadBytes: 1 << 20,
		Projects: map[string]config.ProjectConfig{
			"example": {Root: root, Mirror: filepath.Join(t.TempDir(), "example.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_worker"},
		},
	}
	svc, _ := mcpServiceWithSQLite(t, cfg)
	token, err := service.EnsureOperatorToken(svc.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	staleAdmin, err := svc.AdminSessionMint(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdminSessionRevoke(staleAdmin.Session); err != nil {
		t.Fatal(err)
	}
	server := &Server{Service: svc}
	payload := mustJSON(t, map[string]any{"project": "EXM", "root": filepath.Join(t.TempDir(), "ignored")})
	unauthorized := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/operator/project/token", bytes.NewReader(payload))
	unauthorized.Host = "127.0.0.1:1"
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorized.Header.Set("X-GPT-Tunnel-Admin-Session", staleAdmin.Session)
	unauthorizedRecorder := httptest.NewRecorder()
	server.Router().ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized operator response=%d body=%s", unauthorizedRecorder.Code, unauthorizedRecorder.Body.String())
	}
	if bytes.Contains(unauthorizedRecorder.Body.Bytes(), []byte(token)) {
		t.Fatal("unauthorized response leaked the operator credential")
	}
	wrong := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/operator/project/token", bytes.NewReader(payload))
	wrong.Host = "127.0.0.1:1"
	wrong.RemoteAddr = "127.0.0.1:1234"
	wrong.Header.Set(operatorTokenHeader, staleAdmin.Session)
	wrongRecorder := httptest.NewRecorder()
	server.Router().ServeHTTP(wrongRecorder, wrong)
	if wrongRecorder.Code != http.StatusForbidden {
		t.Fatalf("invalid operator response=%d body=%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}
	authorized := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/operator/project/token", bytes.NewReader(payload))
	authorized.Host = "127.0.0.1:1"
	authorized.RemoteAddr = "127.0.0.1:1234"
	authorized.Header.Set(operatorTokenHeader, token)
	authorizedRecorder := httptest.NewRecorder()
	server.Router().ServeHTTP(authorizedRecorder, authorized)
	if authorizedRecorder.Code != http.StatusOK {
		t.Fatalf("authorized operator response=%d body=%s", authorizedRecorder.Code, authorizedRecorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(authorizedRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["token"] == "" || result["project_id"] != "example" {
		t.Fatalf("authorized operator result=%#v", response)
	}
}
