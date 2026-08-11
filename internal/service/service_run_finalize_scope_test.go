package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestRunFinalizeUsesServerResolvedDocsOnlyScope(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "scoped-finalize")
	publishServerOwnedChange(t, s.Config.Projects["example"].Root, run.Branch, "docs/scope.md", "docs-only scoped finalization")
	var resolved gates.TestScope
	s.gateExecutorWithScope = func(_ context.Context, _ string, names []string, scope gates.TestScope) ([]model.CompletionGateResult, error) {
		resolved = scope
		return fakeReceiptResults(names), nil
	}
	if _, result, err := s.RunFinalize(context.Background(), FinalizeInput{
		RunID:   run.ID,
		Summary: "docs-only scope",
	}); err != nil || result.Status != "TASK_FINALIZED" {
		t.Fatalf("scoped finalization failed: result=%#v err=%v", result, err)
	}
	if resolved.Mode != gates.TestScopePackages || len(resolved.Packages) != 0 {
		t.Fatalf("docs-only finalization did not use an explicitly safe empty package scope: %#v", resolved)
	}
}

func TestResolveFinalizationTestScopeUsesFullForBroadAndAmbiguousChanges(t *testing.T) {
	for _, operationClass := range []string{"integration", "activation", "release", "unknown"} {
		scope := resolveFinalizationTestScope(context.Background(), operationClass, t.TempDir(), []string{"internal/service/service.go"})
		if scope.Mode != gates.TestScopeFull {
			t.Fatalf("operation class %q did not require full scope: %#v", operationClass, scope)
		}
	}
	scope := resolveFinalizationTestScope(context.Background(), "implementation", t.TempDir(), []string{"go.mod"})
	if scope.Mode != gates.TestScopeFull {
		t.Fatalf("ambiguous infrastructure change did not fall back to full scope: %#v", scope)
	}
}

func TestResolveFinalizationTestScopeKeepsSingleAndMultiPackageScopes(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string][]string{
		"single": {"internal/gates/gates.go"},
		"multi":  {"internal/gates/gates.go", "internal/service/service_gates.go"},
	} {
		t.Run(name, func(t *testing.T) {
			scope := resolveFinalizationTestScope(context.Background(), "implementation", root, changed)
			if scope.Mode != gates.TestScopePackages || len(scope.Packages) == 0 {
				t.Fatalf("Go scope was not package-scoped: %#v", scope)
			}
			for _, want := range []string{"./internal/gates", "./internal/service"} {
				if name == "single" && want == "./internal/service" {
					continue
				}
				found := false
				for _, got := range scope.Packages {
					if got == want {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("scope %#v did not contain %q", scope.Packages, want)
				}
			}
		})
	}
}

func TestRunFinalizeScopedGateFailureDoesNotPublishState(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "scoped-gate-failure")
	publishServerOwnedChange(t, s.Config.Projects["example"].Root, run.Branch, "docs/scope.md", "scoped gate failure")
	s.gateExecutorWithScope = func(context.Context, string, []string, gates.TestScope) ([]model.CompletionGateResult, error) {
		return nil, errors.New("scoped test failed")
	}
	ctx := context.Background()
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID:   run.ID,
		Summary: "scoped failure",
	}); err == nil {
		t.Fatal("scoped gate failure was accepted")
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("scoped gate failure published state: before=%s after=%s err=%v", before, after, err)
	}
	if _, err := s.RunReport(ctx, run.ID); err == nil {
		t.Fatal("scoped gate failure created a report")
	}
}

func TestRunFinalizeRejectsDirtyWorktreeBeforeScopedGates(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "dirty-finalize")
	root := s.Config.Projects["example"].Root
	if err := os.WriteFile(filepath.Join(root, "dirty-finalize.txt"), []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.gateExecutorWithScope = func(context.Context, string, []string, gates.TestScope) ([]model.CompletionGateResult, error) {
		calls++
		return fakeReceiptResults([]string{"format", "check", "test"}), nil
	}
	ctx := context.Background()
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID:   run.ID,
		Summary: "dirty finalization",
	}); err == nil || !strings.Contains(err.Error(), "worktree must be clean") {
		t.Fatalf("dirty finalization was not rejected before scoped gates: %v", err)
	}
	if calls != 0 {
		t.Fatalf("scoped gates ran for a dirty worktree: %d", calls)
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || after != before {
		t.Fatalf("dirty finalization mutated Hub: before=%s after=%s err=%v", before, after, err)
	}
}
