package activation

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk529RunScript(t *testing.T, dir, inputPath, outputPath, liveStateDir string, input map[string]any) (string, error) {
	t.Helper()
	repoRoot := tsk627RepoRoot(t)
	envelope, err := json.Marshal(map[string]any{"input": input, "context": map[string]any{"project": "gpt-tunnel-gateway"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, envelope, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", filepath.Join(repoRoot, "scripts", "release-prod.py"))
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

// TestTSK529ReleaseProdRejectsMismatchedAuthorityBeforeSideEffect runs the
// real script against a real checkout and durable Track fixture: a source the
// accepted Track does not bind must fail before any tag or push, and no
// structured output may be emitted.
func TestTSK529ReleaseProdRejectsMismatchedAuthorityBeforeSideEffect(t *testing.T) {
	clone, inputPath, outputPath, liveStateDir := tsk606Fixture(t)
	head := tsk627Git(t, clone, "rev-parse", "HEAD")
	tree := tsk627Git(t, clone, "rev-parse", "HEAD^{tree}")
	// The Track accepts a *different* source than the checkout under test.
	tsk606SeedAcceptedTrack(t, liveStateDir, strings.Repeat("9", 40), strings.Repeat("8", 40), 8)
	stderr, err := tsk529RunScript(t, clone, inputPath, outputPath, liveStateDir, map[string]any{
		"track": "GTW-TRK2", "source_commit": head, "source_tree": tree,
	})
	if err == nil {
		t.Fatal("release_prod ran against a mismatched Track authority")
	}
	if !strings.Contains(stderr, "authority_bind") {
		t.Fatalf("mismatch did not fail at the authority boundary: %s", stderr)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatal("rejected release emitted structured output")
	}
}

// TestTSK529ReleaseProdRejectsStaleAcceptance proves that a source newer than
// the accepted review head cannot be released — acceptance binds exactly.
func TestTSK529ReleaseProdRejectsStaleAcceptance(t *testing.T) {
	clone, inputPath, outputPath, liveStateDir := tsk606Fixture(t)
	acceptedHead := tsk627Git(t, clone, "rev-parse", "HEAD")
	tsk606SeedAcceptedTrack(t, liveStateDir, acceptedHead, strings.Repeat("8", 40), 8)
	// Advance the checkout beyond the accepted source.
	tsk627Git(t, clone, "config", "user.name", "test")
	tsk627Git(t, clone, "config", "user.email", "test@example.com")
	tsk627Git(t, clone, "commit", "--allow-empty", "-m", "advance beyond acceptance")
	newHead := tsk627Git(t, clone, "rev-parse", "HEAD")
	newTree := tsk627Git(t, clone, "rev-parse", "HEAD^{tree}")
	stderr, err := tsk529RunScript(t, clone, inputPath, outputPath, liveStateDir, map[string]any{
		"track": "GTW-TRK2", "source_commit": newHead, "source_tree": newTree,
	})
	if err == nil {
		t.Fatal("release_prod released source newer than the accepted review")
	}
	if !strings.Contains(stderr, "authority_bind") {
		t.Fatalf("stale acceptance did not fail at the authority boundary: %s", stderr)
	}
}

// TestTSK529ReleaseProdScriptUnitTests runs the repository-owned script unit
// tests covering the authority/rejection and remote-identity surfaces.
func TestTSK529ReleaseProdScriptUnitTests(t *testing.T) {
	repoRoot := tsk627RepoRoot(t)
	command := exec.Command("python3", "-m", "unittest", "scripts/release_prod_test.py")
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("release_prod unit tests failed: %v\n%s", err, output)
	}
}

// TestTSK529ReleaseProdDefinitionIsCanonical pins the Procedure contract:
// typed Track/source input authority and the fixed bounded check set.
func TestTSK529ReleaseProdDefinitionIsCanonical(t *testing.T) {
	output, err := model.ReleaseProdProcedureOutputSchema(model.ReleaseProdProcedureChecks)
	if err != nil {
		t.Fatal(err)
	}
	definition := model.ProjectProcedureDefinition{
		Script: "scripts/release-prod.py", Summary: "release", Guide: "release",
		Input: model.ReleaseProdProcedureInputSchema(), Output: output,
	}
	if err := model.ValidateProjectProcedureDefinition(definition); err != nil {
		t.Fatalf("release_prod definition is invalid: %v", err)
	}
	input := model.ReleaseProdProcedureInputSchema()
	properties, _ := input["properties"].(map[string]any)
	for _, field := range []string{"track", "source_commit", "source_tree"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("release_prod input lacks %s", field)
		}
	}
	// Compile through the real call-time contract path: source fingerprints
	// and tag_object must be GitFingerprint refs or the Procedure is
	// uncallable once installed.
	compiled := compileTSK686Procedure(t, model.ReleaseProdProcedureName, definition)
	if compiled.Output == nil {
		t.Fatal("release_prod compiled without an output contract")
	}
}
