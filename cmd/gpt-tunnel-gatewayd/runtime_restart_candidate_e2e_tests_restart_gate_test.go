package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCandidateGatewayRestartMCPNetworkE2E(t *testing.T) {
	candidate := os.Getenv("GTW_CANDIDATE_GATEWAY_BINARY")
	wantSource := os.Getenv("GTW_CANDIDATE_SOURCE_SHA")
	if candidate == "" || wantSource == "" {
		t.Skip("set GTW_CANDIDATE_GATEWAY_BINARY and GTW_CANDIDATE_SOURCE_SHA for candidate E2E")
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		t.Fatal(err)
	}
	gotSource, err := exec.Command(resolvedCandidate, "--source-sha").Output()
	if err != nil {
		t.Fatalf("candidate source identity: %v", err)
	}
	if strings.TrimSpace(string(gotSource)) != wantSource {
		t.Fatalf("candidate source=%q want %q", strings.TrimSpace(string(gotSource)), wantSource)
	}

	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	pidDir := filepath.Join(stateDir, "pids")
	logDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	listenAddr := reserveCandidateListenAddr(t)
	configPath := filepath.Join(root, "config.json")
	c := config.Config{
		SchemaVersion: 1, GatewayID: "r2-candidate", ListenAddr: listenAddr, StateDir: stateDir,
		MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100,
		DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: "true",
		Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"},
		Controller: config.ControllerConfig{
			GatewayBinary: resolvedCandidate, TunnelClientBinary: "/usr/bin/sleep", PIDDir: pidDir,
			LogDir: logDir, TunnelHealthListenAddr: "127.0.0.1:18766",
		},
		Projects: map[string]config.ProjectConfig{
			"example": {Root: projectRoot, Mirror: filepath.Join(root, "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "candidate-agent"},
		},
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapService := service.NewWithDurability(c, db)
	if _, err := bootstrapService.ProjectRegister(context.Background(), service.ProjectRegisterInput{
		Project: model.Project{
			SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git",
			DefaultBranch: "main", WorkflowRepository: "planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active",
		},
		WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead},
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	tunnel := exec.Command("/bin/sleep", "60")
	if err := tunnel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tunnel.Process.Kill()
		_ = tunnel.Wait()
	})
	if err := os.WriteFile(filepath.Join(pidDir, "tunnel.pid"), []byte(strconv.Itoa(tunnel.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	tunnelPID := tunnel.Process.Pid

	gateway := exec.Command(resolvedCandidate, "--config", configPath)
	gateway.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	initialPID := gateway.Process.Pid
	t.Cleanup(func() {
		killCandidatePID(t, initialPID)
		if pid := readCandidatePID(filepath.Join(pidDir, "gateway.pid")); pid > 0 && pid != initialPID {
			killCandidatePID(t, pid)
		}
	})
	if err := waitCandidateHTTP(listenAddr, "/readyz", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "gateway.pid"), []byte(strconv.Itoa(initialPID)), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err = sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	session, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &candidateMCPClient{client: &http.Client{Timeout: 10 * time.Second}, endpoint: "http://" + listenAddr + "/mcp"}
	first, err := client.call(session.ID, "runtime/restart", map[string]any{"operation_id": "candidate-restart-once"})
	if err != nil {
		t.Fatal(err)
	}
	if first.StatusCode != http.StatusOK {
		t.Fatalf("runtime/restart status=%d body=%s", first.StatusCode, first.Body)
	}
	if first.ContentLength != int64(len(first.Body)) {
		t.Fatalf("runtime/restart Content-Length=%d body_bytes=%d", first.ContentLength, len(first.Body))
	}
	if outcome := candidateMCPOutcome(t, first.Body); outcome != "accepted" {
		t.Fatalf("runtime/restart outcome=%q body=%s", outcome, first.Body)
	}

	newPID := waitCandidatePIDChange(filepath.Join(pidDir, "gateway.pid"), initialPID, 10*time.Second)
	if newPID < 1 {
		t.Fatalf("replacement Gateway PID did not appear; initial=%d", initialPID)
	}
	if err := waitCandidateHTTP(listenAddr, "/readyz", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if newPID == initialPID {
		t.Fatalf("Gateway PID did not change: %d", newPID)
	}
	if got := readCandidatePID(filepath.Join(pidDir, "tunnel.pid")); got != tunnelPID {
		t.Fatalf("Tunnel PID=%d want unchanged %d", got, tunnelPID)
	}
	postRestart, err := client.request("ping", map[string]any{})
	if err != nil || postRestart.StatusCode != http.StatusOK {
		t.Fatalf("post-restart MCP ping status=%d err=%v body=%s", postRestart.StatusCode, err, postRestart.Body)
	}
	waitCandidateRecoverySuccess(t, client, session.ID, "candidate-restart-once", 10*time.Second)

	second, err := client.call(session.ID, "runtime/restart", map[string]any{"operation_id": "candidate-restart-once"})
	if err != nil {
		t.Fatal(err)
	}
	if second.StatusCode != http.StatusOK || candidateMCPOutcome(t, second.Body) != "succeeded" {
		t.Fatalf("duplicate runtime/restart status=%d body=%s", second.StatusCode, second.Body)
	}
	if got := readCandidatePID(filepath.Join(pidDir, "gateway.pid")); got != newPID {
		t.Fatalf("duplicate operation changed Gateway PID from %d to %d", newPID, got)
	}
	if got := readCandidatePID(filepath.Join(pidDir, "tunnel.pid")); got != tunnelPID {
		t.Fatalf("duplicate operation changed Tunnel PID from %d to %d", tunnelPID, got)
	}
	t.Logf("candidate_source=%s gateway_pid_before=%d gateway_pid_after=%d tunnel_pid=%d", wantSource, initialPID, newPID, tunnelPID)
}
