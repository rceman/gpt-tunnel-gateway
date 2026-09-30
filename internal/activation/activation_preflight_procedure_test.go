package activation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// tsk627RepoRoot resolves the repository root from this file's location.
func tsk627RepoRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../.."))
}

func tsk627Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// tsk627Fixture builds the procedure environment: a clean disposable clone of
// the repository, a live-state fixture seeded with real durable databases,
// and the Procedure envelope paths the script reads.
func tsk627Fixture(t *testing.T) (clone, inputPath, outputPath, liveStateDir string) {
	t.Helper()
	repoRoot := tsk627RepoRoot(t)
	clone = filepath.Join(t.TempDir(), "source")
	cloneCmd := exec.Command("git", "clone", "--local", "--quiet", repoRoot, clone)
	cloneCmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if output, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("clone fixture: %v: %s", err, output)
	}
	liveStateDir = t.TempDir()
	db, err := sqlitestore.Open(liveStateDir)
	if err != nil {
		t.Fatalf("open live fixture: %v", err)
	}
	// Seed the GTW configuration so the candidate boots against realistic
	// durable state and runs the activation_preflight install migration on
	// the snapshot copy only.
	ctx := context.Background()
	configuration := model.DefaultProjectConfiguration("gpt-tunnel-gateway", time.Now().UTC())
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, configuration.ProjectID, configuration.Revision, payload, configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(liveStateDir, "procedure-runs", "run-fixture")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	inputPath = filepath.Join(runDir, "input.json")
	outputPath = filepath.Join(runDir, "output.json")
	return clone, inputPath, outputPath, liveStateDir
}

func tsk627RunScript(t *testing.T, repoRoot, dir, inputPath, outputPath, runDir string, input map[string]any) (string, error) {
	t.Helper()
	if input != nil {
		envelope, err := json.Marshal(map[string]any{"input": input, "context": map[string]any{"project": "gpt-tunnel-gateway"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inputPath, envelope, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("python3", filepath.Join(repoRoot, "scripts", "activation-preflight.py"))
	command.Dir = dir
	command.Env = []string{
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"TMPDIR=" + runDir,
		"GTW_PROCEDURE_INPUT_FILE=" + inputPath,
		"GTW_PROCEDURE_OUTPUT_FILE=" + outputPath,
	}
	output, runErr := command.CombinedOutput()
	return string(output), runErr
}

func tsk627Fingerprints(t *testing.T, dir string) (string, string) {
	return tsk627Git(t, dir, "rev-parse", "HEAD")[:8], tsk627Git(t, dir, "rev-parse", "HEAD^{tree}")[:8]
}

func TestTSK627ActivationPreflightScriptEndToEnd(t *testing.T) {
	repoRoot := tsk627RepoRoot(t)
	clone, inputPath, outputPath, liveStateDir := tsk627Fixture(t)
	head, tree := tsk627Fingerprints(t, clone)
	sharedPath, localPath := sqlitestore.Paths(liveStateDir)
	digest := func(path string) string {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		sum := sha256.New()
		if _, err := io.Copy(sum, file); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(sum.Sum(nil))
	}
	liveSharedDigest, liveLocalDigest := digest(sharedPath), digest(localPath)
	runDir := filepath.Join(liveStateDir, "procedure-runs", "run-fixture")
	output, err := tsk627RunScript(t, repoRoot, clone, inputPath, outputPath, runDir, map[string]any{
		"source_commit": head, "source_tree": tree,
	})
	if err != nil {
		t.Fatalf("activation_preflight failed: %v\n%s", err, output)
	}
	var evidence struct {
		SourceCommit   string `json:"source_commit"`
		SourceTree     string `json:"source_tree"`
		GatewayVersion string `json:"gateway_version"`
		Checks         []struct {
			ID       string `json:"id"`
			ExitCode int    `json:"exit_code"`
		} `json:"checks"`
	}
	raw, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read structured output: %v", readErr)
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatalf("structured output is invalid: %v", err)
	}
	wantVersion := strings.TrimSpace(string(mustRead(t, filepath.Join(repoRoot, "VERSION"))))
	if evidence.GatewayVersion != wantVersion {
		t.Fatalf("evidence version %q != %q", evidence.GatewayVersion, wantVersion)
	}
	cloneHead := tsk627Git(t, clone, "rev-parse", "HEAD")
	if evidence.SourceCommit != cloneHead {
		t.Fatalf("evidence is not bound to the exact source commit: %q != %q", evidence.SourceCommit, cloneHead)
	}
	if len(evidence.Checks) != len(model.ActivationPreflightProcedureChecks) {
		t.Fatalf("evidence checks %v", evidence.Checks)
	}
	for index, name := range model.ActivationPreflightProcedureChecks {
		if evidence.Checks[index].ID != name || evidence.Checks[index].ExitCode != 0 {
			t.Fatalf("check %d incomplete: %+v", index, evidence.Checks[index])
		}
	}
	// The live databases were never mounted writable: they must be untouched.
	if digest(sharedPath) != liveSharedDigest || digest(localPath) != liveLocalDigest {
		t.Fatal("live durable databases were mutated by the preflight")
	}
}

func TestTSK627ActivationPreflightRejectsStaleSource(t *testing.T) {
	repoRoot := tsk627RepoRoot(t)
	clone, inputPath, outputPath, liveStateDir := tsk627Fixture(t)
	runDir := filepath.Join(liveStateDir, "procedure-runs", "run-fixture")
	output, err := tsk627RunScript(t, repoRoot, clone, inputPath, outputPath, runDir, map[string]any{
		"source_commit": "00000000", "source_tree": "00000000",
	})
	if err == nil {
		t.Fatalf("stale source was accepted: %s", output)
	}
	if !strings.Contains(output, "source_bind") {
		t.Fatalf("stale-source failure did not identify the check: %s", output)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatal("failure produced structured evidence")
	}
}

func TestTSK627ActivationPreflightRejectsDirtySource(t *testing.T) {
	repoRoot := tsk627RepoRoot(t)
	clone, inputPath, outputPath, liveStateDir := tsk627Fixture(t)
	if err := os.WriteFile(filepath.Join(clone, "dirty.marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	head, tree := tsk627Fingerprints(t, clone)
	runDir := filepath.Join(liveStateDir, "procedure-runs", "run-fixture")
	output, err := tsk627RunScript(t, repoRoot, clone, inputPath, outputPath, runDir, map[string]any{
		"source_commit": head, "source_tree": tree,
	})
	if err == nil {
		t.Fatalf("dirty source was accepted: %s", output)
	}
	if !strings.Contains(output, "source_bind") {
		t.Fatalf("dirty-source failure did not identify the check: %s", output)
	}
}

func TestTSK627ActivationPreflightCandidateMigrationFailure(t *testing.T) {
	repoRoot := tsk627RepoRoot(t)
	clone, inputPath, outputPath, liveStateDir := tsk627Fixture(t)
	// Corrupt a Shared upgrade marker so the candidate's migration fails at
	// open — the script must fail closed before any evidence is emitted.
	db, err := sqlitestore.Open(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), `UPDATE shared_upgrade_migrations SET state='corrupt' WHERE migration_id='project_configuration_retired_fields_v3'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	head, tree := tsk627Fingerprints(t, clone)
	runDir := filepath.Join(liveStateDir, "procedure-runs", "run-fixture")
	output, err := tsk627RunScript(t, repoRoot, clone, inputPath, outputPath, runDir, map[string]any{
		"source_commit": head, "source_tree": tree,
	})
	if err == nil {
		t.Fatalf("candidate migration failure was not reported: %s", output)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatal("migration failure produced structured evidence")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
