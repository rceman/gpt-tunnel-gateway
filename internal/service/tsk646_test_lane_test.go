package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK646TaskTestFullProfileUsesOnlyDeterministicRunner(t *testing.T) {
	commands := model.DefaultProjectGateCommands()
	for _, argv := range [][]string{commands.Test.Task.Command} {
		if len(argv) != 1 || argv[0] != "./scripts/test-full.sh" {
			t.Fatalf("default task test gate command=%v, want the deterministic ./scripts/test-full.sh runner", argv)
		}
		for _, arg := range argv {
			if arg == "-race" || arg == "-tags" || arg == "-tags=livee2e" || arg == "-tags=liveperformance" {
				t.Fatalf("full test gate command selects a specialist lane: %v", argv)
			}
		}
		if !taskExecutionVerificationFullSuiteArgv(argv) {
			t.Fatalf("default test gate command is not a valid full-suite argv: %v", argv)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "scripts", "test-full.sh")); err != nil {
		t.Fatalf("deterministic full runner script missing: %v", err)
	}
	for _, lane := range []string{"test-race.sh", "test-e2e.sh", "test-performance.py", "test-profile.py"} {
		if _, err := os.Stat(filepath.Join("..", "..", "scripts", lane)); err != nil {
			t.Fatalf("specialist lane script %s missing: %v", lane, err)
		}
	}
}
