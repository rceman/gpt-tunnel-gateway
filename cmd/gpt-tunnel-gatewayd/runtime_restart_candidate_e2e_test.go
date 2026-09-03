package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
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

	session, err := durableSession.NewStore(stateDir).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleDelivery, SessionType: durableSession.SessionTypeChatGPT,
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

func TestCandidateDebugActivateMCPNetworkE2E(t *testing.T) {
	const activationTimeout = 90 * time.Second
	candidate := os.Getenv("GTW_CANDIDATE_GATEWAY_BINARY")
	wantSource := os.Getenv("GTW_CANDIDATE_SOURCE_SHA")
	sourceRoot := os.Getenv("GTW_CANDIDATE_SOURCE_ROOT")
	if candidate == "" || wantSource == "" || sourceRoot == "" {
		t.Skip("set GTW_CANDIDATE_GATEWAY_BINARY, GTW_CANDIDATE_SOURCE_SHA, and GTW_CANDIDATE_SOURCE_ROOT for debug activation E2E")
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
	if len(wantSource) != 40 {
		t.Fatalf("candidate source is not an exact commit: %q", wantSource)
	}

	root := t.TempDir()
	sourceFixture := filepath.Join(root, "source")
	testutil.Git(t, root, "clone", "--local", sourceRoot, sourceFixture)
	testutil.Git(t, sourceFixture, "checkout", "-b", "main", wantSource)
	if got := strings.TrimSpace(testutil.Git(t, sourceFixture, "rev-parse", "HEAD")); got != wantSource {
		t.Fatalf("source fixture HEAD=%q want %q", got, wantSource)
	}
	if status := strings.TrimSpace(testutil.Git(t, sourceFixture, "status", "--porcelain", "--untracked-files=all")); status != "" {
		t.Fatalf("source fixture is dirty: %q", status)
	}

	hubBare, _, _ := testutil.RepoWithBareRemote(t)
	stateDir := filepath.Join(root, "state")
	pidDir := filepath.Join(stateDir, "pids")
	logDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	installDir := filepath.Join(root, "installed")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range releaseartifacts.BinaryNames {
		from := filepath.Join(filepath.Dir(resolvedCandidate), name)
		data, readErr := os.ReadFile(from)
		if readErr != nil {
			t.Fatalf("read candidate artifact %s: %v", name, readErr)
		}
		to := filepath.Join(installDir, name)
		if err := os.WriteFile(to, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	tunnelScript := filepath.Join(root, "tunnel")
	if err := os.WriteFile(tunnelScript, []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	tunnel := exec.Command(tunnelScript, "run")
	if err := tunnel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killCandidatePID(t, tunnel.Process.Pid) })
	tunnelPID := tunnel.Process.Pid
	if err := os.WriteFile(filepath.Join(pidDir, "tunnel.pid"), []byte(strconv.Itoa(tunnelPID)), 0o600); err != nil {
		t.Fatal(err)
	}

	var tunnelRequests atomic.Int64
	var failSecondHealth atomic.Bool
	tunnelHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		request := tunnelRequests.Add(1)
		if failSecondHealth.Load() && request >= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(tunnelHealth.Close)
	tunnelHealthAddr := strings.TrimPrefix(tunnelHealth.URL, "http://")

	listenAddr := reserveCandidateListenAddr(t)
	configPath := filepath.Join(root, "config.json")
	c := config.Config{
		SchemaVersion: 1, GatewayID: "debug-e2e", ListenAddr: listenAddr, StateDir: stateDir,
		MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100,
		DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: "true",
		Debug: config.DebugConfig{Enabled: true},
		Hub:   config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"},
		Controller: config.ControllerConfig{
			GatewayBinary: filepath.Join(installDir, "gpt-tunnel-gatewayd"), TunnelClientBinary: tunnelScript,
			PIDDir: pidDir, LogDir: logDir, TunnelHealthListenAddr: tunnelHealthAddr,
		},
		Projects: map[string]config.ProjectConfig{
			"gpt-tunnel-gateway": {Root: sourceFixture, Mirror: filepath.Join(root, "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "GTW", AirelaySessionKey: "debug-e2e"},
		},
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	gateway := exec.Command(filepath.Join(installDir, "gpt-tunnel-gatewayd"), "--config", configPath)
	gateway.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+configPath)
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	initialPID := gateway.Process.Pid
	t.Cleanup(func() {
		killCandidatePID(t, initialPID)
		if pid := readCandidatePID(filepath.Join(pidDir, "gateway.pid")); pid > 0 {
			killCandidatePID(t, pid)
		}
	})
	if err := waitCandidateHTTP(listenAddr, "/readyz", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "gateway.pid"), []byte(strconv.Itoa(initialPID)), 0o600); err != nil {
		t.Fatal(err)
	}

	session, err := durableSession.NewStore(stateDir).CreateUnbound(durableSession.RolePlanner, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &candidateMCPClient{client: &http.Client{Timeout: 20 * time.Second}, endpoint: "http://" + listenAddr + "/mcp"}
	first, err := client.call(session.ID, "debug/activate", map[string]any{"main_sha": wantSource})
	if err != nil {
		t.Fatalf("debug/activate response failed: %v", err)
	}
	if first.StatusCode != http.StatusOK {
		t.Fatalf("debug/activate success status=%d body=%s", first.StatusCode, first.Body)
	}
	if first.ContentLength != int64(len(first.Body)) {
		t.Fatalf("debug/activate success Content-Length=%d body_bytes=%d", first.ContentLength, len(first.Body))
	}
	firstResult := candidateMCPStructured(t, first.Body)
	if firstResult["source_head"] != wantSource || firstResult["activation"] != "accepted" || firstResult["outcome"] != "accepted" {
		t.Fatalf("debug/activate success result=%#v", firstResult)
	}
	newPID := waitCandidatePIDChange(filepath.Join(pidDir, "gateway.pid"), initialPID, activationTimeout)
	if newPID < 1 || newPID == initialPID {
		t.Fatalf("successful debug activation did not replace Gateway: old=%d new=%d", initialPID, newPID)
	}
	if err := waitCandidateHTTP(listenAddr, "/readyz", activationTimeout); err != nil {
		t.Fatal(err)
	}
	assertInstalledCandidate(t, installDir, wantSource, "0.6.14")
	if got := readCandidatePID(filepath.Join(pidDir, "tunnel.pid")); got != tunnelPID {
		t.Fatalf("debug activation changed Tunnel PID record: got=%d want=%d", got, tunnelPID)
	}
	if !processExists(tunnelPID) {
		t.Fatal("Tunnel process exited during successful Gateway-only activation")
	}
	waitCandidateDebugActivationSuccess(t, client, session.ID, wantSource, activationTimeout)

	artifactBefore := make(map[string]string, len(releaseartifacts.BinaryNames))
	for _, name := range releaseartifacts.BinaryNames {
		hash, hashErr := releaseartifacts.HashFile(filepath.Join(installDir, name))
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		artifactBefore[name] = hash
	}
	testutil.Git(t, sourceFixture, "-c", "user.name=debug-e2e", "-c", "user.email=debug-e2e@example.invalid", "commit", "--allow-empty", "-m", "debug-activation-failure")
	failureSource := strings.TrimSpace(testutil.Git(t, sourceFixture, "rev-parse", "HEAD"))
	if failureSource == wantSource || len(failureSource) != 40 {
		t.Fatalf("failure source=%q want a distinct exact commit", failureSource)
	}
	failSecondHealth.Store(true)
	tunnelRequests.Store(0)
	beforeRollbackPID := readCandidatePID(filepath.Join(pidDir, "gateway.pid"))
	rollback, err := client.call(session.ID, "debug/activate", map[string]any{"main_sha": failureSource})
	if err != nil {
		t.Fatalf("debug/activate rollback response failed: %v", err)
	}
	if rollback.StatusCode != http.StatusOK {
		t.Fatalf("debug/activate rollback status=%d body=%s", rollback.StatusCode, rollback.Body)
	}
	if rollback.ContentLength != int64(len(rollback.Body)) {
		t.Fatalf("debug/activate rollback Content-Length=%d body_bytes=%d", rollback.ContentLength, len(rollback.Body))
	}
	rollbackResult := candidateMCPStructured(t, rollback.Body)
	if rollbackResult["source_head"] != failureSource || rollbackResult["activation"] != "accepted" || rollbackResult["outcome"] != "accepted" {
		t.Fatalf("debug/activate rollback result=%#v", rollbackResult)
	}
	rolledBackPID := waitCandidateArtifactRestore(t, installDir, artifactBefore, filepath.Join(pidDir, "gateway.pid"), beforeRollbackPID, activationTimeout)
	if rolledBackPID < 1 || rolledBackPID == beforeRollbackPID {
		if entries, walkErr := os.ReadDir(logDir); walkErr == nil {
			for _, entry := range entries {
				data, readErr := os.ReadFile(filepath.Join(logDir, entry.Name()))
				if readErr == nil {
					t.Logf("debug activation log %s: %s", entry.Name(), data)
				}
			}
		}
		t.Fatalf("rollback did not restart the restored Gateway: before=%d after=%d", beforeRollbackPID, rolledBackPID)
	}
	if err := waitCandidateHTTP(listenAddr, "/readyz", activationTimeout); err != nil {
		t.Fatal(err)
	}
	waitCandidateDebugActivationFailureReceipt(t, stateDir, failureSource, activationTimeout)
	terminalFailure, err := client.call(session.ID, "debug/activate", map[string]any{"main_sha": failureSource})
	if err != nil {
		t.Fatalf("debug/activate terminal failure response returned EOF/transport error: %v", err)
	}
	if terminalFailure.StatusCode != http.StatusOK {
		t.Fatalf("debug/activate terminal failure status=%d body=%s", terminalFailure.StatusCode, terminalFailure.Body)
	}
	var terminalEnvelope map[string]any
	if err := json.Unmarshal(terminalFailure.Body, &terminalEnvelope); err != nil {
		t.Fatalf("debug/activate terminal failure JSON: %v: %s", err, terminalFailure.Body)
	}
	terminalResult, _ := terminalEnvelope["result"].(map[string]any)
	terminalStructured, _ := terminalResult["structuredContent"].(map[string]any)
	terminalError, _ := terminalStructured["error"].(map[string]any)
	if terminalStructured["ok"] != false || terminalError["code"] != "GATEWAY_DEBUG_ACTIVATION_FAILED" {
		t.Fatalf("debug/activate terminal failure=%#v", terminalEnvelope)
	}
	for _, name := range releaseartifacts.BinaryNames {
		got, hashErr := releaseartifacts.HashFile(filepath.Join(installDir, name))
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		if got != artifactBefore[name] {
			t.Fatalf("rollback artifact %s hash=%s want restored %s", name, got, artifactBefore[name])
		}
	}
	if !processExists(tunnelPID) {
		t.Fatal("Tunnel process exited during Gateway rollback")
	}
	postRollback, err := client.request("initialize", map[string]any{})
	if err != nil || postRollback.StatusCode != http.StatusOK {
		t.Fatalf("post-rollback MCP status=%d err=%v body=%s", postRollback.StatusCode, err, postRollback.Body)
	}
	t.Logf("debug_activation_source=%s gateway_pid_initial=%d gateway_pid_success=%d gateway_pid_rollback=%d tunnel_pid=%d tunnel_health_requests=%d", wantSource, initialPID, newPID, rolledBackPID, tunnelPID, tunnelRequests.Load())
}

func assertInstalledCandidate(t *testing.T, installDir, source, version string) {
	t.Helper()
	for _, name := range releaseartifacts.BinaryNames {
		path := filepath.Join(installDir, name)
		gotSource, _, err := releaseartifacts.BinarySourceRevision(path)
		if err != nil || gotSource != source {
			t.Fatalf("installed %s source=%q err=%v want %q", name, gotSource, err, source)
		}
		gotVersion, err := releaseartifacts.BinaryVersion(path)
		if err != nil || gotVersion != version {
			t.Fatalf("installed %s version=%q err=%v want %q", name, gotVersion, err, version)
		}
	}
}

func waitCandidateArtifactRestore(t *testing.T, installDir string, want map[string]string, pidPath string, oldPID int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pid := readCandidatePID(pidPath)
		if pid > 0 && pid != oldPID && processExists(pid) {
			matches := true
			for name, expected := range want {
				got, err := releaseartifacts.HashFile(filepath.Join(installDir, name))
				if err != nil || got != expected {
					matches = false
					break
				}
			}
			if matches {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

type candidateMCPResponse struct {
	StatusCode    int
	ContentLength int64
	Body          []byte
}

type candidateMCPClient struct {
	client   *http.Client
	endpoint string
	nextID   int
}

func (c *candidateMCPClient) request(method string, params any) (candidateMCPResponse, error) {
	c.nextID++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	if err != nil {
		return candidateMCPResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return candidateMCPResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return candidateMCPResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return candidateMCPResponse{StatusCode: resp.StatusCode, ContentLength: resp.ContentLength, Body: data}, err
}

func (c *candidateMCPClient) call(sessionID, action string, input map[string]any) (candidateMCPResponse, error) {
	return c.request("tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": action, "input": input},
	})
}

func candidateMCPOutcome(t *testing.T, body []byte) string {
	t.Helper()
	value := candidateMCPStructured(t, body)
	outcome, _ := value["outcome"].(string)
	return outcome
}

func candidateMCPStructured(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("MCP response JSON: %v: %s", err, body)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP response result=%#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("MCP structuredContent=%#v", response)
	}
	value, ok := structured["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP structured result=%#v", response)
	}
	return value
}

func waitCandidateDebugActivationSuccess(t *testing.T, client *candidateMCPClient, sessionID, sourceHead string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.call(sessionID, "debug/activate", map[string]any{"main_sha": sourceHead})
		if err != nil {
			t.Fatalf("debug/activate terminal success probe returned EOF/transport error: %v", err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("debug/activate terminal success status=%d body=%s", response.StatusCode, response.Body)
		}
		value := candidateMCPStructured(t, response.Body)
		if value["source_head"] != sourceHead {
			t.Fatalf("debug/activate terminal success source=%#v want %s", value["source_head"], sourceHead)
		}
		if value["outcome"] == "succeeded" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("debug activation %s did not reach terminal success", sourceHead)
}

func waitCandidateDebugActivationFailureReceipt(t *testing.T, stateDir, sourceHead string, timeout time.Duration) {
	t.Helper()
	id := "gateway-debug-activation-" + sourceHead
	digest := sha256.Sum256([]byte(id))
	path := filepath.Join(stateDir, "gateway-debug-activation", hex.EncodeToString(digest[:])+".json")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var receipt struct {
				OperationID string `json:"operation_id"`
				SourceHead  string `json:"source_head"`
				Outcome     string `json:"outcome"`
				Error       string `json:"error"`
			}
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatalf("debug activation terminal receipt JSON: %v", err)
			}
			if receipt.OperationID != id || receipt.SourceHead != sourceHead {
				t.Fatalf("unexpected debug activation terminal receipt=%#v", receipt)
			}
			if receipt.Outcome == "failed" {
				if receipt.Error == "" || len(receipt.Error) > 2048 {
					t.Fatalf("unexpected debug activation terminal receipt=%#v", receipt)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("debug activation terminal failure receipt did not settle within %s", timeout)
}

func waitCandidateRecoverySuccess(t *testing.T, client *candidateMCPClient, sessionID, operationID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.call(sessionID, "runtime/logs", map[string]any{"limit": 100, "operation_id": operationID})
		if err == nil && response.StatusCode == http.StatusOK {
			result := candidateMCPStructured(t, response.Body)
			if events, ok := result["events"].([]any); ok {
				for _, raw := range events {
					event, ok := raw.(map[string]any)
					if ok && event["event"] == "recovery_finish" && event["operation_id"] == operationID && event["message"] == "succeeded" {
						return
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("recovery operation %q did not reach succeeded terminal event", operationID)
}

func reserveCandidateListenAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func waitCandidateHTTP(listenAddr, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + listenAddr + path)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("candidate HTTP %s did not become ready within %s", path, timeout)
}

func waitCandidatePIDChange(path string, oldPID int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid := readCandidatePID(path); pid > 0 && pid != oldPID && processExists(pid) {
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

func readCandidatePID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var record struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(data, &record) == nil {
			return record.PID
		}
		return 0
	}
	pid, _ := strconv.Atoi(trimmed)
	return pid
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func killCandidatePID(t *testing.T, pid int) {
	t.Helper()
	if pid < 1 || !processExists(pid) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processExists(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
