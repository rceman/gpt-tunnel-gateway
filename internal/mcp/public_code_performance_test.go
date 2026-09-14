//go:build liveperformance

package mcp

import (
	"testing"
	"time"
)

func TestPublicCodeLatencyPerformanceProfile(t *testing.T) {
	fixture := newPublicCodeE2EFixture(t)
	harness := newPublicCodeCallHarness(t, fixture)
	started := time.Now()
	harness.call(t, "code/search", map[string]any{
		"worktree": fixture.mainSelector,
		"query":    "needle",
		"live":     true,
	})
	elapsed := time.Since(started)
	t.Logf("public code/search: %dms", elapsed.Milliseconds())
	if elapsed >= time.Second {
		t.Fatalf("public code/search exceeded one second: %s", elapsed)
	}
}
