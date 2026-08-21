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
)

func TestSmokeCandidateReachesHTTPReadyWithinExistingDeadline(t *testing.T) {
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
	fixtureStateDir := t.TempDir()
	fixtureDB, err := sqlitestore.Open(fixtureStateDir)
	if err != nil {
		t.Fatal("open isolated fixture store:", err)
	}
	t.Cleanup(func() { _ = fixtureDB.Close() })
	fixtureHubLock, err := lockfile.Acquire(filepath.Join(fixtureStateDir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal("hold isolated fixture Hub lock:", err)
	}
	t.Cleanup(func() { _ = fixtureHubLock.Release() })
	c := config.Config{
		SchemaVersion:          1,
		GatewayID:              "candidate_test",
		ListenAddr:             "127.0.0.1:18877",
		StateDir:               fixtureStateDir,
		MaxReadBytes:           1 << 20,
		MaxDiffBytes:           1 << 20,
		MaxListItems:           100,
		DispatchTimeoutSeconds: 5,
		RunTimeoutSeconds:      60,
		AirelayCommand:         "/bin/true",
		Hub: config.HubConfig{
			RepositoryURL: "http://127.0.0.1:1/forbidden-production-hub.git",
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
	if _, err := fixtureDB.Shared.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "candidate-smoke-fixture", 1, []byte("ok"), time.Now().UTC()); err != nil {
		t.Fatalf("isolated fixture store unusable after candidate smoke: %v", err)
	}
}
