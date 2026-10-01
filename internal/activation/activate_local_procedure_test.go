package activation

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// tsk606Fixture builds the procedure environment: a clean disposable clone of
// the repository, a live-state fixture with durable databases, and the
// Procedure envelope paths the script reads.
func tsk606Fixture(t *testing.T) (clone, inputPath, outputPath, liveStateDir string) {
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

func tsk606SeedAcceptedTrack(t *testing.T, liveStateDir, head, tree string, revision int64) {
	t.Helper()
	db, err := sqlitestore.Open(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	track := map[string]any{
		"schema_version": 1, "id": "GTW-TRK2", "project_id": "gpt-tunnel-gateway",
		"revision": revision, "milestone": "GTW-MIL1", "title": "Example Track",
		"tasks": []string{"GTW-TSK1"}, "status": "accepted",
		"review": map[string]any{
			"head": head, "tree": tree, "digest": strings.Repeat("c", 64),
			"track_revision": revision,
			"tasks":          []any{map[string]any{"key": "GTW-TSK1", "revision": 1, "revision_sha256": strings.Repeat("d", 64)}},
			"submitted_at":   "2026-09-30T00:00:00Z", "submitted_by": "HOM_GTW_L_test",
		},
		"created_by": "planner", "created_at": "2026-09-30T00:00:00Z",
		"updated_by": "planner", "updated_at": "2026-09-30T00:00:00Z",
	}
	payload, err := json.Marshal(track)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), `INSERT INTO shared_tracks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "GTW-TRK2", revision, payload, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func tsk606RunScript(t *testing.T, dir, inputPath, outputPath, liveStateDir string, input map[string]any) (string, error) {
	t.Helper()
	repoRoot := tsk627RepoRoot(t)
	envelope, err := json.Marshal(map[string]any{"input": input, "context": map[string]any{"project": "gpt-tunnel-gateway"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, envelope, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", filepath.Join(repoRoot, "scripts", "activate-local.py"))
	command.Dir = dir
	command.Env = []string{
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"TMPDIR=" + filepath.Join(liveStateDir, "procedure-runs", "run-fixture"),
		"GTW_PROCEDURE_INPUT_FILE=" + inputPath,
		"GTW_PROCEDURE_OUTPUT_FILE=" + outputPath,
	}
	stderr := &strings.Builder{}
	command.Stderr = stderr
	runErr := command.Run()
	return stderr.String(), runErr
}

// TestTSK606ActivateLocalRejectsMismatchedAuthorityBeforeMutation runs the
// real script against a real checkout and durable Track fixture: a source
// that the accepted Track does not bind must fail before any live mutation,
// and no structured output may be emitted.
func TestTSK606ActivateLocalRejectsMismatchedAuthorityBeforeMutation(t *testing.T) {
	clone, inputPath, outputPath, liveStateDir := tsk606Fixture(t)
	head := tsk627Git(t, clone, "rev-parse", "HEAD")
	tree := tsk627Git(t, clone, "rev-parse", "HEAD^{tree}")
	// The Track accepts a *different* source than the checkout under test.
	tsk606SeedAcceptedTrack(t, liveStateDir, strings.Repeat("9", 40), strings.Repeat("8", 40), 8)
	stderr, err := tsk606RunScript(t, clone, inputPath, outputPath, liveStateDir, map[string]any{
		"track": "GTW-TRK2", "source_commit": head, "source_tree": tree,
	})
	if err == nil {
		t.Fatal("activate_local ran against a mismatched Track authority")
	}
	if !strings.Contains(stderr, "authority_bind") {
		t.Fatalf("mismatch did not fail at the authority boundary: %s", stderr)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatal("rejected activation emitted structured output")
	}
}

// TestTSK606ActivateLocalRejectsMissingTrackBeforeMutation proves a missing
// accepted Track fails before any build, preflight, or live mutation.
func TestTSK606ActivateLocalRejectsMissingTrackBeforeMutation(t *testing.T) {
	clone, inputPath, outputPath, liveStateDir := tsk606Fixture(t)
	head := tsk627Git(t, clone, "rev-parse", "HEAD")
	tree := tsk627Git(t, clone, "rev-parse", "HEAD^{tree}")
	stderr, err := tsk606RunScript(t, clone, inputPath, outputPath, liveStateDir, map[string]any{
		"track": "GTW-TRK2", "source_commit": head, "source_tree": tree,
	})
	if err == nil {
		t.Fatal("activate_local ran without an accepted Track")
	}
	if !strings.Contains(stderr, "authority_bind") {
		t.Fatalf("missing authority did not fail at the authority boundary: %s", stderr)
	}
}

// TestTSK606ActivateLocalScriptUnitTests runs the repository-owned script unit
// tests covering the authority/rejection and failure-evidence surfaces.
func TestTSK606ActivateLocalScriptUnitTests(t *testing.T) {
	repoRoot := tsk627RepoRoot(t)
	command := exec.Command("python3", "-m", "unittest", "scripts/activate_local_test.py")
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("activate_local unit tests failed: %v\n%s", err, output)
	}
}

// TestTSK606ActivateLocalDefinitionIsCanonical pins the Procedure contract:
// typed Track/source input authority and the fixed bounded check set.
func TestTSK606ActivateLocalDefinitionIsCanonical(t *testing.T) {
	output, err := model.ActivateLocalProcedureOutputSchema(model.ActivateLocalProcedureChecks)
	if err != nil {
		t.Fatal(err)
	}
	definition := model.ProjectProcedureDefinition{
		Script: "scripts/activate-local.py", Summary: "activate", Guide: "activate",
		Input: model.ActivateLocalProcedureInputSchema(), Output: output,
	}
	if err := model.ValidateProjectProcedureDefinition(definition); err != nil {
		t.Fatalf("activate_local definition is invalid: %v", err)
	}
	input := model.ActivateLocalProcedureInputSchema()
	properties, _ := input["properties"].(map[string]any)
	for _, field := range []string{"track", "source_commit", "source_tree"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("activate_local input lacks %s", field)
		}
	}
	required, _ := input["required"].([]any)
	if len(required) != 3 {
		t.Fatalf("activate_local required fields = %v", required)
	}
}
