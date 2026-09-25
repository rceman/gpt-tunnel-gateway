package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestOperatorAdminSessionRoutesMintAndRevokeThroughDaemonAuthority(t *testing.T) {
	svc, _ := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM"})
	token, err := service.EnsureOperatorToken(svc.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Service: svc}
	call := func(path, payload string, credential string) (int, map[string]any) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1"+path, bytes.NewBufferString(payload))
		request.Host = "127.0.0.1:1"
		request.RemoteAddr = "127.0.0.1:1234"
		if credential != "" {
			request.Header.Set(operatorTokenHeader, credential)
		}
		recorder := httptest.NewRecorder()
		server.Router().ServeHTTP(recorder, request)
		var response map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode operator response: %v: %s", err, recorder.Body.String())
		}
		return recorder.Code, response
	}
	if status, _ := call(operatorAdminMintPath, `{"label":"cli-test"}`, ""); status != http.StatusUnauthorized {
		t.Fatalf("missing credential status=%d", status)
	}
	if status, _ := call(operatorAdminMintPath, `{"label":"cli-test"}`, "invalid"); status != http.StatusForbidden {
		t.Fatalf("invalid credential status=%d", status)
	}
	if status, _ := call(operatorAdminMintPath, `{"label":"cli-test"}`, token+" "); status != http.StatusForbidden {
		t.Fatalf("non-canonical credential status=%d", status)
	}
	oversized := `{"label":"` + strings.Repeat("x", operatorRequestLimit) + `"}`
	if status, _ := call(operatorAdminMintPath, oversized, token); status != http.StatusBadRequest {
		t.Fatalf("oversized request status=%d", status)
	}
	if status, _ := call(operatorAdminMintPath, `{"label":"cli-test","unexpected":true}`, token); status != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", status)
	}
	store := durableSession.NewStoreWithGateway(svc.Durability, "HOM")
	planner, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	plannerPayload, err := json.Marshal(map[string]string{"session": planner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := call(operatorAdminRevokePath, string(plannerPayload), token); status != http.StatusConflict {
		t.Fatalf("non-Admin Session revoke status=%d", status)
	}
	plannerAfter, err := store.Get(planner.ID)
	if err != nil || plannerAfter.Status != durableSession.StatusActive {
		t.Fatalf("non-Admin Session changed after revoke request: %#v err=%v", plannerAfter, err)
	}
	status, response := call(operatorAdminMintPath, `{"label":"cli-test"}`, token)
	if status != http.StatusOK {
		t.Fatalf("mint status=%d response=%#v", status, response)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["status"] != durableSession.StatusActive {
		t.Fatalf("mint result=%#v", response)
	}
	id, _ := result["session"].(string)
	if id == "" {
		t.Fatal("mint returned no Admin Session identifier")
	}
	record, err := svc.AdminSession(context.Background(), id)
	if err != nil || record.Role != durableSession.RoleAdmin || record.Status != durableSession.StatusActive {
		t.Fatalf("minted Admin Session=%#v err=%v", record, err)
	}
	status, response = call(operatorAdminRevokePath, `{"session":"`+id+`"}`, token)
	if status != http.StatusOK {
		t.Fatalf("revoke status=%d response=%#v", status, response)
	}
	record, err = svc.AdminSession(context.Background(), id)
	if err != nil || record.Status != durableSession.StatusEnded {
		t.Fatalf("revoked Admin Session=%#v err=%v", record, err)
	}
}

func TestOperatorRouteSurfacesBoundedSanitizedFailure(t *testing.T) {
	svc, _ := mcpServiceWithSQLite(t, config.Config{GatewayID: "HOM"})
	token, err := service.EnsureOperatorToken(svc.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerOperatorJSONRoute(mux, "/operator/test", &Server{Service: svc}, func(context.Context, struct{}) (any, error) {
		return nil, errors.New(token + "\n" + strings.Repeat("daemon detail ", 80))
	})
	request := httptest.NewRequest(http.MethodPost, "/operator/test", bytes.NewBufferString(`{}`))
	request.Header.Set(operatorTokenHeader, token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode operator failure: %v: %s", err, recorder.Body.String())
	}
	if recorder.Code != http.StatusConflict || !strings.HasPrefix(response.Error.Message, "[redacted] daemon detail") || strings.Contains(response.Error.Message, token) || strings.ContainsAny(response.Error.Message, "\r\n\t") || len(response.Error.Message) > operatorFailureMessageLimit {
		t.Fatalf("operator failure was not safely surfaced: status=%d message=%q", recorder.Code, response.Error.Message)
	}
}
