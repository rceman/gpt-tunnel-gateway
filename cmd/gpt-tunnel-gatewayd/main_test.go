package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestHoldHubRepositoryLockHelper(t *testing.T) {
	if os.Getenv("GTW_HOLD_HUB_LOCK") != "1" {
		return
	}
	dir := os.Getenv("GTW_HOLD_HUB_LOCK_DIR")
	lock, err := lockfile.Acquire(filepath.Join(dir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.ReadAll(os.Stdin)
}

func testBootstrapConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0", MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100,
		Hub: config.HubConfig{RepositoryURL: filepath.Join(t.TempDir(), "missing-hub"), Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
	}
}

func TestBootstrapGTWIdentityMigrationDoesNotRequireHub(t *testing.T) {
	c := testBootstrapConfig(t)
	c.Projects = map[string]config.ProjectConfig{
		config.GTWProjectID: {
			Root:              t.TempDir(),
			Mirror:            filepath.Join(t.TempDir(), "mirror.git"),
			Remote:            "origin",
			DefaultBranch:     "main",
			ProjectCode:       "GTW",
			AirelaySessionKey: "gpt-tunnel-gateway_master",
		},
	}
	c.ProjectAgentBindings = map[string]map[string]config.AgentBinding{
		config.GTWProjectID: {
			config.GTWWorkerAgentID: {SessionKey: "gpt-tunnel-gateway_master", Profile: "coding"},
		},
	}
	db, err := sqlitestore.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	agent := model.Agent{
		SchemaVersion:        model.AgentSchemaVersion,
		ProjectID:            config.GTWProjectID,
		AgentID:              config.LegacyGTWWorkerAgentID,
		Role:                 model.AgentRoleCoding,
		Enabled:              true,
		RecommendedReasoning: model.ReasoningHigh,
		Capabilities:         []string{"coding"},
		CreatedAt:            now.Add(-time.Minute),
		UpdatedAt:            now,
	}
	payload, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(context.Background(), sqlitestore.LocalAgent{ProjectID: agent.ProjectID, AgentID: agent.AgentID, Payload: payload, UpdatedAt: now.Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTaskExecutionState(context.Background(), model.TaskExecutionState{
		TaskID: "GTW-TSK620", ProjectID: config.GTWProjectID, TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionDispatched, Stage: "code", Worktree: "WT-TSK620-cccccccc", BaseHead: strings.Repeat("b", 40),
		Head: strings.Repeat("c", 40), Branch: "task/GTW-TSK620-bootstrap", Agent: config.LegacyGTWWorkerAgentID,
		ExecutionRevision: 1, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err := bootstrapGateway(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBootstrap(t, runtime)
	if _, err := runtime.service.Durability.ReadLocalAgent(context.Background(), config.GTWProjectID, config.LegacyGTWWorkerAgentID); err == nil {
		t.Fatal("startup left the legacy Local Agent projection active")
	}
	if _, err := runtime.service.Durability.ReadLocalAgent(context.Background(), config.GTWProjectID, config.GTWWorkerAgentID); err != nil {
		t.Fatalf("startup did not migrate Local Agent projection without Hub: %v", err)
	}
	state, found, err := runtime.service.Durability.ReadTaskExecutionState(context.Background(), config.GTWProjectID, "GTW-TSK620")
	if err != nil || !found || state.Agent != config.GTWWorkerAgentID {
		t.Fatalf("startup did not migrate nonterminal execution authority without Hub: state=%#v found=%v err=%v", state, found, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := postReadyHubSyncContext(runtime.service, ctx, nil); err == nil {
		t.Fatal("unavailable Hub was reported as reconciled")
	}
	if _, err := runtime.service.Durability.ReadLocalAgent(context.Background(), config.GTWProjectID, config.GTWWorkerAgentID); err != nil {
		t.Fatalf("Hub outage removed the Local Agent continuity: %v", err)
	}
}

func closeBootstrap(t *testing.T, runtime *gatewayRuntime) {
	t.Helper()
	if runtime == nil {
		return
	}
	_ = runtime.server.Close()
	_ = runtime.durability.Close()
}

func TestBootstrapReachesHTTPReadyWithHubLockHeldByAnotherProcess(t *testing.T) {
	c := testBootstrapConfig(t)
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHoldHubRepositoryLockHelper$")
	cmd.Env = append(os.Environ(), "GTW_HOLD_HUB_LOCK=1", "GTW_HOLD_HUB_LOCK_DIR="+c.StateDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("lock helper readiness=%q err=%v", line, err)
	}
	runtime, err := bootstrapGateway(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBootstrap(t, runtime)
	response, err := http.Get("http://" + runtime.listener.Addr().String() + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("readyz status=%d", response.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := postReadyHubSyncContext(runtime.service, ctx, nil); err == nil {
		t.Fatal("held Hub lock was reported as healthy")
	}
	result, err := runtime.service.Durability.Local.Query(context.Background(), "SELECT 1")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(1) {
		t.Fatalf("local store unavailable while Hub sync was degraded: result=%#v err=%v", result, err)
	}
}

func TestPostReadyHubUnavailableDegradesWithoutBlockingLocalStore(t *testing.T) {
	runtime, err := bootstrapGateway(testBootstrapConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBootstrap(t, runtime)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := postReadyHubSyncContext(runtime.service, ctx, nil); err == nil {
		t.Fatal("Hub failure was reported as success")
	}
	result, err := runtime.service.Durability.Local.Query(context.Background(), "SELECT 1")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(1) {
		t.Fatalf("local store unavailable after degraded Hub sync: result=%#v err=%v", result, err)
	}
}

func TestPostReadyHubSyncLoopRetriesExpiredAttemptUntilHubEnsureCompletes(t *testing.T) {
	oldTimeout, oldDelays := postReadyHubAttemptTimeout, postReadyHubRetryDelays
	postReadyHubAttemptTimeout = time.Millisecond
	postReadyHubRetryDelays = []time.Duration{time.Millisecond}
	t.Cleanup(func() {
		postReadyHubAttemptTimeout = oldTimeout
		postReadyHubRetryDelays = oldDelays
	})

	attempts := 0
	hubEnsureComplete := false
	err := postReadyHubSyncLoop(context.Background(), func(string) {}, func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		hubEnsureComplete = true
		return nil
	}, func(context.Context) error {
		if !hubEnsureComplete {
			t.Fatal("state check ran before Hub ensure completed")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("persistent retry returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
}

func TestBootstrapFailsBeforeHTTPReadyOnCorruptSQLite(t *testing.T) {
	c := testBootstrapConfig(t)
	sharedPath := filepath.Join(c.StateDir, "databases", "shared.db")
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sharedPath, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrapGateway(c, nil); err == nil {
		t.Fatal("corrupt SQLite state reached bootstrap readiness")
	}
}

func TestGatewayHTTPServerAllowsBoundedDelayedActionResponse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("ok"))
	})
	server := newGatewayHTTPServer("127.0.0.1:0", handler)
	if server.WriteTimeout != 0 {
		t.Fatalf("gateway transport has a fixed write timeout: %s", server.WriteTimeout)
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + listener.Addr().String())
		if err != nil {
			done <- result{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		done <- result{
			body: string(body),
			err:  err,
		}
	}()
	<-started
	close(release)
	select {
	case got := <-done:
		if got.err != nil || got.body != "ok" {
			t.Fatalf("delayed action response failed: body=%q err=%v", got.body, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("delayed action response did not complete")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}
