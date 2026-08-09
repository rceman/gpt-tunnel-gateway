package onboarding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func TestManagedProjectsLockRetriesWithContextBound(t *testing.T) {
	stateDir := t.TempDir()
	held, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "managed-projects")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := acquireManagedProjectsLock(ctx, stateDir); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended managed lock error = %v, want context deadline", err)
	}
}
