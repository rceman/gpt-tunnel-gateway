package gates

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveTestScopeMapsNestedAndDeletedGoFilesDeterministically(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/zeta", "internal/alpha"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var discovered []string
	discover := func(_ context.Context, _ string, target string) error {
		discovered = append(discovered, target)
		return nil
	}
	scope, err := resolveTestScope(context.Background(), root, []string{
		"internal/zeta/z.go", "internal/alpha/a.go", "internal/zeta/other.go", "internal/deleted/deleted.go",
	}, discover)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scope.Packages, []string{"./internal/alpha", "./internal/deleted", "./internal/zeta"}) {
		t.Fatalf("packages=%v", scope.Packages)
	}
	if !reflect.DeepEqual(discovered, []string{"./internal/zeta", "./internal/alpha", "./internal/zeta"}) {
		t.Fatalf("discovery calls=%v", discovered)
	}
}

func TestResolveTestScopeSafeDocsAndAmbiguousInputs(t *testing.T) {
	root := t.TempDir()
	discover := func(context.Context, string, string) error { return nil }
	docs, err := resolveTestScope(context.Background(), root, []string{"README.md", "docs/guide.md"}, discover)
	if err != nil || docs.Mode != TestScopePackages || len(docs.Packages) != 0 {
		t.Fatalf("safe docs scope=%#v err=%v", docs, err)
	}
	for _, path := range []string{"go.mod", "go.sum", "Makefile", "scripts/check.sh", "notes.json"} {
		scope, err := resolveTestScope(context.Background(), root, []string{path}, discover)
		if err == nil || scope.Mode != TestScopeFull {
			t.Fatalf("ambiguous %s did not fall back: scope=%#v err=%v", path, scope, err)
		}
	}
}

func TestResolveTestScopeResolverFailureFallsBackToFull(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	scope, err := resolveTestScope(context.Background(), root, []string{"internal/broken/file.go"}, func(context.Context, string, string) error {
		return errors.New("package discovery unavailable")
	})
	if err == nil || scope.Mode != TestScopeFull || scope.FallbackReason != "" {
		t.Fatalf("resolver failure scope=%#v err=%v", scope, err)
	}
}

func TestTestScopeCommandArgsAndIdentityAreCanonical(t *testing.T) {
	full, err := FullTestScope().CommandArgs()
	if err != nil || !reflect.DeepEqual(full, []string{"go", "test", "./...", "-count=1"}) {
		t.Fatalf("full args=%v err=%v", full, err)
	}
	scope := TestScope{Mode: TestScopePackages, Packages: []string{"./z", "./a", "./z"}}
	args, err := scope.CommandArgs()
	if err != nil || !reflect.DeepEqual(args, []string{"go", "test", "./a", "./z", "-count=1"}) {
		t.Fatalf("scoped args=%v err=%v", args, err)
	}
	identity, err := scope.CommandIdentity()
	if err != nil || identity == "" {
		t.Fatalf("scoped identity=%q err=%v", identity, err)
	}
	empty, err := (TestScope{Mode: TestScopePackages}).CommandArgs()
	if err != nil || empty != nil {
		t.Fatalf("empty safe scope args=%v err=%v", empty, err)
	}
	if _, err := (TestScope{Mode: TestScopePackages, Packages: []string{"./internal/gates;rm"}}).CommandArgs(); err == nil {
		t.Fatal("package target accepted shell punctuation")
	}
}
