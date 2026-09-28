package activation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestCandidatePreflightRejectsTSK602LegacyStateBeforeCutover(t *testing.T) {
	gatewayPath, version := buildCandidateGateway(t)
	stateDir := t.TempDir()
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	canonical, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		t.Fatal(err)
	}
	fields["activation_profile_ref"] = json.RawMessage(`"retired-activation-profile"`)
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), `UPDATE shared_upgrade_migrations SET state=? WHERE migration_id=?`, "in_progress", "project_configuration_retired_fields_v3"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, configuration.ProjectID, configuration.Revision, legacy, configuration.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, localPath := sqlitestore.Paths(stateDir)
	for _, path := range []string{localPath, localPath + "-wal", localPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	var requests atomic.Int32
	oldGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("old gateway ready"))
	}))
	defer oldGateway.Close()
	candidateStateDir := t.TempDir()
	if err := os.Chmod(candidateStateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	c := candidatePreflightConfig(stateDir, strings.TrimPrefix(oldGateway.URL, "http://"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = smokeCandidateAtStateDir(ctx, c, gatewayPath, version, candidateStateDir)
	if err == nil || !strings.Contains(err.Error(), "activation_profile_ref") || !strings.Contains(err.Error(), "SQLITE_LOCAL_MIGRATION") {
		t.Fatalf("candidate preflight error=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("candidate preflight contacted the old Gateway listener %d times", requests.Load())
	}
	response, err := http.Get(oldGateway.URL)
	if err != nil {
		t.Fatalf("old Gateway stopped serving after rejected preflight: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || requests.Load() != 1 {
		t.Fatalf("old Gateway status=%d requests=%d", response.StatusCode, requests.Load())
	}

	_, candidateLocalPath := sqlitestore.Paths(candidateStateDir)
	local, err := upstream.Open(upstream.Config{Path: candidateLocalPath})
	if err != nil {
		t.Fatal("open candidate Local database after Shared rejection:", err)
	}
	localSchema, err := local.Query(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='local_callback_epochs'`)
	if err != nil || len(localSchema.Rows) != 1 || len(localSchema.Rows[0]) != 1 {
		t.Fatalf("candidate Local migration did not create the epoch table: rows=%#v err=%v", localSchema, err)
	}
	schema, ok := localSchema.Rows[0][0].(string)
	if !ok || !strings.Contains(schema, "session_id") || !strings.Contains(schema, "operation_id") || !strings.Contains(schema, "hook_outcome") {
		t.Fatalf("candidate Local migration did not complete: schema=%q", schema)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}

	sharedPath, sourceLocalPath := sqlitestore.Paths(stateDir)
	if _, err := os.Stat(sourceLocalPath); !os.IsNotExist(err) {
		t.Fatalf("preflight mutated live Local state: stat err=%v", err)
	}
	shared, err := upstream.Open(upstream.Config{Path: sharedPath})
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	marker, err := shared.Query(ctx, `SELECT state FROM shared_upgrade_migrations WHERE migration_id=?`, "project_configuration_retired_fields_v3")
	if err != nil || len(marker.Rows) != 1 || marker.Rows[0][0] != "in_progress" {
		t.Fatalf("preflight mutated live Shared marker: rows=%#v err=%v", marker, err)
	}
	configurationRows, err := shared.Query(ctx, `SELECT payload FROM shared_project_configurations WHERE id=?`, configuration.ProjectID)
	if err != nil || len(configurationRows.Rows) != 1 || string(configurationRows.Rows[0][0].([]byte)) != string(legacy) {
		t.Fatalf("preflight mutated live Shared ProjectConfiguration: rows=%#v err=%v", configurationRows, err)
	}
}

func TestCandidatePreflightRejectsLocalMigrationMarkerWithoutMutatingLiveState(t *testing.T) {
	gatewayPath, version := buildCandidateGateway(t)
	stateDir := t.TempDir()
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rows, err := db.Local.Query(ctx, `SELECT MAX(version) FROM schema_migrations`)
	if err != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		t.Fatalf("read Local migration marker rows=%#v err=%v", rows, err)
	}
	latestVersion, ok := rows.Rows[0][0].(int64)
	if !ok || latestVersion <= 0 {
		t.Fatalf("latest Local migration version=%#v", rows.Rows[0][0])
	}
	const invalidName = "incompatible-local-migration-marker"
	if _, err := db.Local.Exec(ctx, `UPDATE schema_migrations SET name=? WHERE version=?`, invalidName, latestVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	sharedPath, liveLocalPath := sqlitestore.Paths(stateDir)
	sharedChecksum, err := releaseartifacts.HashFile(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	localChecksum, err := releaseartifacts.HashFile(liveLocalPath)
	if err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	oldGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer oldGateway.Close()
	candidateStateDir := t.TempDir()
	if err := os.Chmod(candidateStateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	candidateCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	config := candidatePreflightConfig(stateDir, strings.TrimPrefix(oldGateway.URL, "http://"))
	err = smokeCandidateAtStateDir(candidateCtx, config, gatewayPath, version, candidateStateDir)
	if err == nil || !strings.Contains(err.Error(), "unsupported migration marker") || !strings.Contains(err.Error(), "SQLITE_LOCAL_MIGRATION") {
		t.Fatalf("Local marker preflight error=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("Local marker preflight contacted the old Gateway %d times", requests.Load())
	}
	response, err := http.Get(oldGateway.URL)
	if err != nil {
		t.Fatalf("old Gateway stopped serving after rejected Local marker: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || requests.Load() != 1 {
		t.Fatalf("old Gateway status=%d requests=%d", response.StatusCode, requests.Load())
	}
	for path, want := range map[string]string{sharedPath: sharedChecksum, liveLocalPath: localChecksum} {
		got, err := releaseartifacts.HashFile(path)
		if err != nil || got != want {
			t.Fatalf("preflight mutated live database %s checksum=%q want=%q err=%v", filepath.Base(path), got, want, err)
		}
	}
	_, candidateLocalPath := sqlitestore.Paths(candidateStateDir)
	candidateLocal, err := upstream.Open(upstream.Config{Path: candidateLocalPath})
	if err != nil {
		t.Fatal(err)
	}
	candidateMarker, err := candidateLocal.Query(ctx, `SELECT name FROM schema_migrations WHERE version=?`, latestVersion)
	if err != nil || len(candidateMarker.Rows) != 1 || candidateMarker.Rows[0][0] != invalidName {
		t.Fatalf("candidate preflight changed Local migration marker rows=%#v err=%v", candidateMarker, err)
	}
	if err := candidateLocal.Close(); err != nil {
		t.Fatal(err)
	}
	liveLocal, err := upstream.Open(upstream.Config{Path: liveLocalPath})
	if err != nil {
		t.Fatal(err)
	}
	defer liveLocal.Close()
	liveMarker, err := liveLocal.Query(ctx, `SELECT name FROM schema_migrations WHERE version=?`, latestVersion)
	if err != nil || len(liveMarker.Rows) != 1 || liveMarker.Rows[0][0] != invalidName {
		t.Fatalf("preflight mutated live Local migration marker rows=%#v err=%v", liveMarker, err)
	}
}

func buildCandidateGateway(t *testing.T) (string, string) {
	t.Helper()
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
	return gatewayPath, strings.TrimSpace(string(versionBytes))
}

func candidatePreflightConfig(stateDir, listenAddr string) config.Config {
	return config.Config{
		SchemaVersion:          1,
		GatewayID:              "HOM",
		ListenAddr:             listenAddr,
		StateDir:               stateDir,
		MaxReadBytes:           1 << 20,
		MaxDiffBytes:           1 << 20,
		MaxListItems:           100,
		DispatchTimeoutSeconds: 5,
		RunTimeoutSeconds:      60,
		AirelayCommand:         "/bin/true",
		Hub: config.HubConfig{
			Branch:      "main",
			AuthorName:  "Gateway Test",
			AuthorEmail: "gateway-test@example.invalid",
		},
		Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:18878"},
		Projects:   map[string]config.ProjectConfig{},
	}
}
