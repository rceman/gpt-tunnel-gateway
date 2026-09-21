package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestOperatorProjectTokenRouteRequiresAdminSessionAndReturnsStaticGrant(t *testing.T) {
	remote, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	svc, db := mcpServiceWithSQLite(t, config.Config{
		GatewayID:    "HOM",
		MaxReadBytes: 1 << 20,
		Projects: map[string]config.ProjectConfig{
			"example": {Root: root, Mirror: filepath.Join(t.TempDir(), "example.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_worker"},
		},
	})
	server := &Server{Service: svc}
	payload := mustJSON(t, map[string]any{"root": root, "remotes": map[string]string{"origin": remote}})
	unauthorized := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/operator/project/token", bytes.NewReader(payload))
	unauthorized.Host = "127.0.0.1:1"
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorizedRecorder := httptest.NewRecorder()
	server.Router().ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized || strings.Contains(unauthorizedRecorder.Body.String(), "gtwbt_") {
		t.Fatalf("unauthorized operator response=%d body=%s", unauthorizedRecorder.Code, unauthorizedRecorder.Body.String())
	}
	admin, err := durableSession.NewStoreWithGateway(db, "HOM").CreateAdmin(nil)
	if err != nil {
		t.Fatal(err)
	}
	authorized := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/operator/project/token", bytes.NewReader(payload))
	authorized.Host = "127.0.0.1:1"
	authorized.RemoteAddr = "127.0.0.1:1234"
	authorized.Header.Set("X-GPT-Tunnel-Admin-Session", admin.ID)
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
