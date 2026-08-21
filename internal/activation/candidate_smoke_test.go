package activation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestSmokeCandidateReachesHTTPReadyWithinExistingDeadline(t *testing.T) {
	_, hubRoot, _ := testutil.RepoWithBareRemote(t)
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../.."))
	versionBytes, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	gatewayPath := filepath.Join(t.TempDir(), "gpt-tunnel-gatewayd")
	build := exec.Command("go", "build", "-o", gatewayPath, "./cmd/gpt-tunnel-gatewayd")
	build.Dir = repoRoot
	var output boundedBuffer
	build.Stdout = &output
	build.Stderr = &output
	if err := build.Run(); err != nil {
		t.Fatalf("build candidate: %v: %s", err, BoundedOutput(output.Bytes()))
	}
	stateDir := t.TempDir()
	productionDB, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal("open production store:", err)
	}
	t.Cleanup(func() { _ = productionDB.Close() })
	productionHubLock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal("hold production Hub lock:", err)
	}
	t.Cleanup(func() { _ = productionHubLock.Release() })
	c := config.Config{
		SchemaVersion:          1,
		GatewayID:              "candidate_test",
		ListenAddr:             "127.0.0.1:18877",
		StateDir:               stateDir,
		MaxReadBytes:           1 << 20,
		MaxDiffBytes:           1 << 20,
		MaxListItems:           100,
		DispatchTimeoutSeconds: 5,
		RunTimeoutSeconds:      60,
		AirelayCommand:         "/bin/true",
		Hub: config.HubConfig{
			RepositoryURL: hubRoot,
			Branch:        "main",
			AuthorName:    "Gateway Test",
			AuthorEmail:   "gateway-test@example.invalid",
		},
		Controller: config.ControllerConfig{
			TunnelHealthListenAddr: "127.0.0.1:18878",
		},
		Projects: map[string]config.ProjectConfig{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), candidateSmokeTimeout+time.Second)
	defer cancel()
	if err := SmokeCandidate(ctx, c, gatewayPath, strings.TrimSpace(string(versionBytes))); err != nil {
		t.Fatal(err)
	}
	if _, err := productionDB.Shared.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "candidate-smoke-production", 1, []byte("ok"), time.Now().UTC()); err != nil {
		t.Fatalf("production store unusable after candidate smoke: %v", err)
	}
}
