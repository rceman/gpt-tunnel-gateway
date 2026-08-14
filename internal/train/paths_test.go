package train

import (
	"path/filepath"
	"testing"
)

func TestCompactHostPathsUseCanonicalProjectCodeAndIDs(t *testing.T) {
	stateDir := t.TempDir()
	worktree, err := CompactWorktreePath(stateDir, "GTW", "GTW-TRN11")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateDir, "work", "GTW", "TRN11"); worktree != want {
		t.Fatalf("worktree path = %q, want %q", worktree, want)
	}
	attempt, err := CompactAttemptPath(stateDir, "GTW", "GTW-TRN11", "GTW-TSK236", 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateDir, "attempts", "GTW", "TRN11", "TSK236", "A2"); attempt != want {
		t.Fatalf("attempt path = %q, want %q", attempt, want)
	}
}

func TestCompactHostPathsRejectMismatchedPrefixes(t *testing.T) {
	if _, err := CompactWorktreePath(t.TempDir(), "GTW", "EXM-TRN11"); err == nil {
		t.Fatal("mismatched train project code was accepted")
	}
	if _, err := CompactAttemptPath(t.TempDir(), "GTW", "GTW-TRN11", "EXM-TSK236", 1); err == nil {
		t.Fatal("mismatched task project code was accepted")
	}
}
