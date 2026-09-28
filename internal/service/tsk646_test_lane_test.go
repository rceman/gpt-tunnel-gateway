package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK646TaskVerificationUsesOrderedProjectProcedureChecks(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "task-verify.py")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("project verification Procedure script missing: %v", err)
	}
	text := string(source)
	checks := []string{
		`("format", ["go", "run", "./cmd/gofmt-struct", "--check", "."])`,
		`("static_check", ["python3", "scripts/static-check.py"])`,
		`("full_test", ["./scripts/test-full.sh"])`,
	}
	previous := -1
	for _, check := range checks {
		position := strings.Index(text, check)
		if position <= previous {
			t.Fatalf("verification checks are absent or out of order: %q", check)
		}
		previous = position
	}
	output, err := model.TaskVerificationProcedureOutputSchema([]string{"format", "static_check", "full_test"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := taskVerificationProcedureGateIDs(output)
	if err != nil || strings.Join(ids, ",") != strings.Join([]string{"format", "static_check", "full_test"}, ",") {
		t.Fatalf("project Procedure result schema ids=%v err=%v", ids, err)
	}
	if _, err := os.Stat(filepath.Join("..", "..", "scripts", "test-full.sh")); err != nil {
		t.Fatalf("canonical full test runner missing: %v", err)
	}
}
