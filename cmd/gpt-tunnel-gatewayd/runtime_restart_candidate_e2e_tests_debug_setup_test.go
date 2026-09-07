package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

type candidateDebugActivationFixture struct {
	t                 *testing.T
	activationTimeout time.Duration
	wantSource        string
	sourceFixture     string
	stateDir          string
	pidDir            string
	logDir            string
	installDir        string
	tunnelPID         int
	initialPID        int
	tunnelRequests    *atomic.Int64
	failSecondHealth  *atomic.Bool
	client            *candidateMCPClient
	sessionID         string
}

func prepareCandidateDebugActivation(t *testing.T) candidateDebugActivationFixture {
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

	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := durableSession.NewStoreWithDurability(db)
	session, err := store.CreateUnbound(durableSession.RolePlanner, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err = store.Bind(session.ID, "gpt-tunnel-gateway", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &candidateMCPClient{client: &http.Client{Timeout: 20 * time.Second}, endpoint: "http://" + listenAddr + "/mcp"}
	return candidateDebugActivationFixture{t: t, activationTimeout: activationTimeout, wantSource: wantSource, sourceFixture: sourceFixture, stateDir: stateDir, pidDir: pidDir, logDir: logDir, installDir: installDir, tunnelPID: tunnelPID, initialPID: initialPID, tunnelRequests: &tunnelRequests, failSecondHealth: &failSecondHealth, client: client, sessionID: session.ID}
}
