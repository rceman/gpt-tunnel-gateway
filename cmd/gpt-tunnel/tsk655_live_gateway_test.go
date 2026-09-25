package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK655LiveGatewayOperatorCLIUsesDaemonOwnedDurability(t *testing.T) {
	gateway := testutil.NewLiveGateway(t, testutil.LiveGatewayHooks{
		BeforeStart: func(gateway *testutil.LiveGateway) {
			if err := (hub.Store{Config: gateway.Config}).Ensure(context.Background()); err != nil {
				t.Fatalf("initialize disposable Hub: %v", err)
			}
		},
	})
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatal("live gateway did not retain daemon SQLite ownership")
	}
	info, err := os.Stat(filepath.Join(gateway.StateDir, service.OperatorTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("daemon operator credential mode=%#o", info.Mode().Perm())
	}
	operatorTokenBefore, err := service.ReadOperatorToken(gateway.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	minted := gateway.MustCLI(t, testutil.LiveCommandOptions{}, "admin", "session", "mint", "--label", "live-test")
	var mint service.AdminSessionResult
	if err := json.Unmarshal([]byte(minted.Stdout), &mint); err != nil {
		t.Fatalf("decode Admin Session mint result: %v\n%s", err, minted.Stdout)
	}
	if mint.Session == "" || mint.Status != durableSession.StatusActive {
		t.Fatalf("Admin Session mint result=%#v", mint)
	}
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatal("operator CLI displaced daemon SQLite ownership")
	}
	gateway.Restart(t)
	if !gateway.OwnerLockActive() {
		t.Fatal("daemon did not retain SQLite ownership after restart")
	}
	operatorTokenAfter, err := service.ReadOperatorToken(gateway.StateDir)
	if err != nil || operatorTokenAfter != operatorTokenBefore {
		t.Fatalf("operator credential did not persist across restart: same=%v err=%v", operatorTokenAfter == operatorTokenBefore, err)
	}
	revoked := gateway.MustCLI(t, testutil.LiveCommandOptions{}, "admin", "session", "revoke", mint.Session)
	var revoke service.AdminSessionResult
	if err := json.Unmarshal([]byte(revoked.Stdout), &revoke); err != nil {
		t.Fatalf("decode Admin Session revoke result: %v\n%s", err, revoked.Stdout)
	}
	if revoke.Session != mint.Session || revoke.Status != durableSession.StatusEnded {
		t.Fatalf("Admin Session revoke result=%#v", revoke)
	}
	staleRequest, err := http.NewRequest(http.MethodPost, "http://"+gateway.ListenAddr()+"/operator/admin/session/mint", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	staleRequest.Header.Set("Content-Type", "application/json")
	staleRequest.Header.Set("X-GPT-Tunnel-Operator-Token", mint.Session)
	staleResponse, err := (&http.Client{Timeout: 3 * time.Second}).Do(staleRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = staleResponse.Body.Close()
	if staleResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked Admin Session used as operator credential returned HTTP %d", staleResponse.StatusCode)
	}
	canonical := gateway.MustCLI(t, testutil.LiveCommandOptions{}, "project", "list")
	var projects map[string]any
	if err := json.Unmarshal([]byte(canonical.Stdout), &projects); err != nil {
		t.Fatalf("canonical operator output is not JSON: %v\n%s", err, canonical.Stdout)
	}
	if _, ok := projects["projects"]; !ok {
		t.Fatalf("canonical operator output=%#v", projects)
	}
	publicProjects, err := gateway.MCPCall(context.Background(), "projects", map[string]any{"gateway": "HOM"})
	if err != nil {
		t.Fatal(err)
	}
	publicGuide, err := gateway.MCPCall(context.Background(), "guide", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	publicProjection, err := json.Marshal([]any{publicProjects, publicGuide})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicProjection), operatorTokenBefore) {
		t.Fatal("operator credential leaked through public MCP projections")
	}
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatal("canonical operator path stopped or displaced daemon ownership")
	}
	evidence, err := json.Marshal(map[string]any{
		"persistence_owner":        "gpt-tunnel-gatewayd",
		"persistence_owner_binary": gateway.GatewayBinary,
		"cli_invoker":              "gpt-tunnel",
		"cli_binary":               gateway.OperatorBinary,
		"owner_lock_active":        gateway.OwnerLockActive(),
		"daemon_running":           gateway.DaemonRunning(),
		"admin_session_mint":       mint.Status,
		"admin_session_revoke":     revoke.Status,
		"canonical_operator_path":  "project list",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Gate-20 evidence: %s", evidence)
	gateway.Stop()
	unavailable := gateway.RunCLI(testutil.LiveCommandOptions{}, "admin", "session", "mint")
	if unavailable.Err == nil || !strings.Contains(unavailable.Stderr, "Gateway daemon/control-plane unavailable") {
		t.Fatalf("daemon-down Admin Session command did not fail closed: err=%v stderr=%q", unavailable.Err, unavailable.Stderr)
	}
	db, err := sqlitestore.Open(gateway.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	records, err := durableSession.NewStoreWithGateway(db, "HOM").List()
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Role == durableSession.RoleAdmin && record.Status == durableSession.StatusActive {
			t.Fatal("daemon-down CLI fallback created an active Admin Session")
		}
	}
	if gateway.DaemonRunning() || gateway.OwnerLockActive() {
		t.Fatal("live gateway teardown left the owner lock active")
	}
}
