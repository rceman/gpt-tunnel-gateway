package gates

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveTestScopeMapsNestedGoFilesDeterministically(t *testing.T) {
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
		"internal/zeta/z.go", "internal/alpha/a.go", "internal/zeta/other.go",
	}, discover)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scope.Packages, []string{"./internal/alpha", "./internal/zeta"}) {
		t.Fatalf("packages=%v", scope.Packages)
	}
	if !reflect.DeepEqual(discovered, []string{"./internal/zeta", "./internal/alpha", "./internal/zeta"}) {
		t.Fatalf("discovery calls=%v", discovered)
	}
}

func TestResolveTestScopeUsesReverseTransitiveDependencyClosure(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"pkg/a", "pkg/b", "pkg/c", "pkg/d"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	graph := packageGraph{
		nodes: map[string]packageGraphNode{
			"./pkg/a": {Target: "./pkg/a", ImportPath: "example/pkg/a"},
			"./pkg/b": {Target: "./pkg/b", ImportPath: "example/pkg/b", Imports: []string{"example/pkg/a"}},
			"./pkg/c": {Target: "./pkg/c", ImportPath: "example/pkg/c", Imports: []string{"example/pkg/b"}},
			"./pkg/d": {Target: "./pkg/d", ImportPath: "example/pkg/d"},
		},
		reverse: map[string]map[string]bool{
			"./pkg/a": {"./pkg/b": true},
			"./pkg/b": {"./pkg/c": true},
		},
	}
	load := func(context.Context, string) (packageGraph, error) { return graph, nil }
	for _, test := range []struct {
		name     string
		changed  []string
		expected []string
	}{
		{name: "a includes all dependents", changed: []string{"pkg/a/a.go", "pkg/a/duplicate.go"}, expected: []string{"./pkg/a", "./pkg/b", "./pkg/c"}},
		{name: "b excludes unrelated and ancestors", changed: []string{"pkg/b/b.go"}, expected: []string{"./pkg/b", "./pkg/c"}},
		{name: "c includes only itself", changed: []string{"pkg/c/c.go"}, expected: []string{"./pkg/c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope, err := resolveTestScopeWithGraph(context.Background(), root, test.changed, load)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(scope.Packages, test.expected) {
				t.Fatalf("packages=%v want=%v", scope.Packages, test.expected)
			}
		})
	}
}

func TestResolveTestScopeFailsClosedForDeletedPackageAndGraphFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := packageGraph{
		nodes: map[string]packageGraphNode{
			"./pkg/a": {Target: "./pkg/a", ImportPath: "example/pkg/a"},
		},
		reverse: map[string]map[string]bool{},
	}
	scope, err := resolveTestScopeWithGraph(context.Background(), root, []string{"pkg/a/removed.go"}, func(context.Context, string) (packageGraph, error) {
		return graph, nil
	})
	if err != nil || scope.Mode != TestScopePackages || !reflect.DeepEqual(scope.Packages, []string{"./pkg/a"}) {
		t.Fatalf("current package deletion scope=%#v err=%v", scope, err)
	}

	scope, err = resolveTestScopeWithGraph(context.Background(), root, []string{"pkg/missing/removed.go"}, func(context.Context, string) (packageGraph, error) {
		return graph, nil
	})
	if err == nil || scope.Mode != TestScopeFull {
		t.Fatalf("vanished package did not fail closed: scope=%#v err=%v", scope, err)
	}
	scope, err = resolveTestScopeWithGraph(context.Background(), root, []string{"pkg/a/a.go"}, func(context.Context, string) (packageGraph, error) {
		return packageGraph{}, errors.New("graph unavailable")
	})
	if err == nil || scope.Mode != TestScopeFull {
		t.Fatalf("graph failure did not fail closed: scope=%#v err=%v", scope, err)
	}
}

func TestResolveTestScopeDocsDoNotRequireGraph(t *testing.T) {
	called := false
	scope, err := resolveTestScopeWithGraph(context.Background(), t.TempDir(), []string{"README.md", "docs/guide.md"}, func(context.Context, string) (packageGraph, error) {
		called = true
		return packageGraph{}, errors.New("must not be called for docs")
	})
	if err != nil || called || scope.Mode != TestScopePackages || len(scope.Packages) != 0 {
		t.Fatalf("docs scope=%#v err=%v called=%v", scope, err, called)
	}
}

func TestLoadPackageGraphUsesCurrentRepositoryPackages(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := loadPackageGraph(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := graph.nodes["./internal/gates"]; !ok {
		t.Fatalf("graph omitted internal/gates: %v", graph.nodes)
	}
	if len(graph.nodes) < 3 {
		t.Fatalf("graph has too few repository packages: %d", len(graph.nodes))
	}
}

func TestResolveTestScopeProductionPathIncludesChangedRepositoryPackage(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	scope, err := ResolveTestScope(context.Background(), root, []string{"internal/gates/test_scope.go"})
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != TestScopePackages {
		t.Fatalf("scope mode=%q reason=%q", scope.Mode, scope.FallbackReason)
	}
	for _, target := range scope.Packages {
		if target == "./internal/gates" {
			return
		}
	}
	t.Fatalf("changed package missing from scope: %v", scope.Packages)
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
	scope := TestScope{
		Mode:     TestScopePackages,
		Packages: []string{"./z", "./a", "./z"},
	}
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
	if _, err := (TestScope{
		Mode:     TestScopePackages,
		Packages: []string{"./internal/gates;rm"},
	}).CommandArgs(); err == nil {
		t.Fatal("package target accepted shell punctuation")
	}
}
