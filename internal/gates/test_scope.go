package gates

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	TestScopeFull     = "full"
	TestScopePackages = "packages"
)

// TestScope is the server-owned, normalized identity of a test invocation.
// Packages are import-directory targets, never caller-provided shell text.
type TestScope struct {
	Mode           string
	Packages       []string
	FallbackReason string
}

func FullTestScope() TestScope {
	return TestScope{Mode: TestScopeFull}
}

func (s TestScope) Normalize() (TestScope, error) {
	switch s.Mode {
	case TestScopeFull:
		return TestScope{
			Mode:           TestScopeFull,
			FallbackReason: s.FallbackReason,
		}, nil
	case TestScopePackages:
		packages := append([]string{}, s.Packages...)
		seen := map[string]bool{}
		for _, packageTarget := range packages {
			if err := validatePackageTarget(packageTarget); err != nil {
				return TestScope{}, err
			}
			if !seen[packageTarget] {
				seen[packageTarget] = true
			}
		}
		packages = packages[:0]
		for packageTarget := range seen {
			packages = append(packages, packageTarget)
		}
		sort.Strings(packages)
		return TestScope{
			Mode:           TestScopePackages,
			Packages:       packages,
			FallbackReason: s.FallbackReason,
		}, nil
	default:
		return TestScope{}, fmt.Errorf("invalid test scope mode %q", s.Mode)
	}
}

// CommandArgs returns the fixed executable arguments for the scope. An empty
// package scope is an explicitly safe no-Go-package test and needs no command.
func (s TestScope) CommandArgs() ([]string, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return nil, err
	}
	if normalized.Mode == TestScopeFull {
		return []string{"go", "test", "./...", "-count=1"}, nil
	}
	if len(normalized.Packages) == 0 {
		return nil, nil
	}
	args := []string{"go", "test"}
	args = append(args, normalized.Packages...)
	return append(args, "-count=1"), nil
}

// CommandIdentity is stable input for a future receipt contract digest.
func (s TestScope) CommandIdentity() (string, error) {
	args, err := s.CommandArgs()
	if err != nil {
		return "", err
	}
	return strings.Join(args, "\x00"), nil
}

// ResolveTestScope conservatively resolves changed repository files. Any
// uncertainty returns a full-suite scope together with the resolver error.
func ResolveTestScope(ctx context.Context, root string, changedFiles []string) (TestScope, error) {
	scope, err := resolveTestScopeWithGraph(ctx, root, changedFiles, loadPackageGraph)
	if err != nil {
		return TestScope{
			Mode:           TestScopeFull,
			FallbackReason: err.Error(),
		}, err
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return TestScope{
			Mode:           TestScopeFull,
			FallbackReason: err.Error(),
		}, err
	}
	return normalized, nil
}

type packageDiscovery func(context.Context, string, string) error

type packageGraphLoader func(context.Context, string) (packageGraph, error)

type packageGraphNode struct {
	Target     string
	ImportPath string
	Imports    []string
}

type packageGraph struct {
	nodes   map[string]packageGraphNode
	reverse map[string]map[string]bool
}

type goListPackage struct {
	Dir          string   `json:"Dir"`
	ImportPath   string   `json:"ImportPath"`
	Imports      []string `json:"Imports"`
	TestImports  []string `json:"TestImports"`
	XTestImports []string `json:"XTestImports"`
	ForTest      string   `json:"ForTest"`
	Module       *struct {
		Main bool `json:"Main"`
	} `json:"Module"`
	Error *struct {
		Err string `json:"Err"`
	} `json:"Error"`
}

func resolveTestScope(ctx context.Context, root string, changedFiles []string, discover packageDiscovery) (TestScope, error) {
	return resolveTestScopeWithGraph(ctx, root, changedFiles, func(ctx context.Context, root string) (packageGraph, error) {
		// This compatibility adapter is retained for focused unit tests that
		// inject the old per-package discovery seam. Production resolution uses
		// loadPackageGraph and therefore computes reverse dependency closure.
		graph := packageGraph{
			nodes:   map[string]packageGraphNode{},
			reverse: map[string]map[string]bool{},
		}
		for _, path := range changedFiles {
			if !strings.HasSuffix(filepath.ToSlash(path), ".go") {
				continue
			}
			dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
			target := packageTarget(dir)
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return packageGraph{}, err
			}
			if err := discover(ctx, root, target); err != nil {
				return packageGraph{}, err
			}
			graph.nodes[target] = packageGraphNode{
				Target:     target,
				ImportPath: target,
			}
		}
		return graph, nil
	})
}

func resolveTestScopeWithGraph(ctx context.Context, root string, changedFiles []string, load packageGraphLoader) (TestScope, error) {
	if strings.TrimSpace(root) == "" {
		return FullTestScope(), fmt.Errorf("test scope root is empty")
	}
	if len(changedFiles) == 0 {
		return TestScope{
			Mode:           TestScopeFull,
			FallbackReason: "changed file set is empty",
		}, fmt.Errorf("changed file set is empty")
	}
	packages := map[string]bool{}
	hasGoChange := false
	for _, path := range changedFiles {
		path = filepath.ToSlash(path)
		if err := model.ValidateRelativePath(path); err != nil {
			return FullTestScope(), fmt.Errorf("changed file %q is invalid: %w", path, err)
		}
		if strings.HasSuffix(path, ".go") {
			hasGoChange = true
			continue
		}
		if isSafeDocumentation(path) {
			continue
		}
		return FullTestScope(), fmt.Errorf("changed file %q has unknown test impact", path)
	}
	if hasGoChange {
		graph, err := load(ctx, root)
		if err != nil {
			return FullTestScope(), fmt.Errorf("build Go package graph: %w", err)
		}
		for _, path := range changedFiles {
			if !strings.HasSuffix(path, ".go") {
				continue
			}
			dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
			target := packageTarget(dir)
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
				return FullTestScope(), fmt.Errorf("inspect Go package directory %q: %w", dir, err)
			}
			if _, ok := graph.nodes[target]; !ok {
				return FullTestScope(), fmt.Errorf("changed Go package %q is not a current package", target)
			}
			packages[target] = true
		}
		queue := make([]string, 0, len(packages))
		for target := range packages {
			queue = append(queue, target)
		}
		for len(queue) > 0 {
			target := queue[0]
			queue = queue[1:]
			for dependent := range graph.reverse[target] {
				if !packages[dependent] {
					packages[dependent] = true
					queue = append(queue, dependent)
				}
			}
		}
	}
	if len(packages) == 0 {
		return TestScope{
			Mode:     TestScopePackages,
			Packages: []string{},
		}, nil
	}
	result := make([]string, 0, len(packages))
	for target := range packages {
		result = append(result, target)
	}
	sort.Strings(result)
	return TestScope{
		Mode:     TestScopePackages,
		Packages: result,
	}, nil
}

func loadPackageGraph(ctx context.Context, root string) (packageGraph, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "-test", "-deps", "./...")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return packageGraph{}, fmt.Errorf("go list exited %d: %s", exit.ExitCode(), strings.TrimSpace(string(exit.Stderr)))
		}
		return packageGraph{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	all := map[string]goListPackage{}
	for {
		var item goListPackage
		err := decoder.Decode(&item)
		if err == io.EOF {
			break
		}
		if err != nil {
			return packageGraph{}, fmt.Errorf("decode go list output: %w", err)
		}
		if item.Error != nil {
			return packageGraph{}, fmt.Errorf("package %q: %s", item.ImportPath, item.Error.Err)
		}
		if item.ImportPath == "" || item.Dir == "" {
			return packageGraph{}, fmt.Errorf("go list returned incomplete package identity")
		}
		if prior, exists := all[item.ImportPath]; exists && prior.Dir != item.Dir {
			return packageGraph{}, fmt.Errorf("ambiguous package %q: %q and %q", item.ImportPath, prior.Dir, item.Dir)
		}
		all[item.ImportPath] = item
	}
	if len(all) == 0 {
		return packageGraph{}, fmt.Errorf("go list returned no packages")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return packageGraph{}, fmt.Errorf("resolve graph root: %w", err)
	}
	graph := packageGraph{
		nodes:   map[string]packageGraphNode{},
		reverse: map[string]map[string]bool{},
	}
	for _, item := range all {
		if item.Module == nil || !item.Module.Main || item.ForTest != "" || strings.HasSuffix(item.ImportPath, ".test") {
			continue
		}
		dir, err := filepath.Abs(item.Dir)
		if err != nil {
			return packageGraph{}, fmt.Errorf("resolve package %q directory: %w", item.ImportPath, err)
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return packageGraph{}, fmt.Errorf("package %q is outside repository root", item.ImportPath)
		}
		target := packageTarget(rel)
		if prior, exists := graph.nodes[target]; exists && prior.ImportPath != item.ImportPath {
			return packageGraph{}, fmt.Errorf("ambiguous package target %q", target)
		}
		imports := append([]string{}, item.Imports...)
		imports = append(imports, item.TestImports...)
		imports = append(imports, item.XTestImports...)
		graph.nodes[target] = packageGraphNode{
			Target:     target,
			ImportPath: item.ImportPath,
			Imports:    imports,
		}
	}
	if len(graph.nodes) == 0 {
		return packageGraph{}, fmt.Errorf("go list returned no repository packages")
	}
	for target, node := range graph.nodes {
		for _, imported := range node.Imports {
			if _, exists := all[imported]; !exists {
				return packageGraph{}, fmt.Errorf("package %q imports unresolved package %q", node.ImportPath, imported)
			}
			for importedTarget, importedNode := range graph.nodes {
				if importedNode.ImportPath != imported {
					continue
				}
				if graph.reverse[importedTarget] == nil {
					graph.reverse[importedTarget] = map[string]bool{}
				}
				graph.reverse[importedTarget][target] = true
			}
		}
	}
	return graph, nil
}

func discoverGoPackage(ctx context.Context, root, target string) error {
	cmd := exec.CommandContext(ctx, "go", "list", "-f", "{{.ImportPath}}", target)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go list %s: %w: %s", target, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func packageTarget(dir string) string {
	dir = filepath.ToSlash(filepath.Clean(dir))
	if dir == "." || dir == "" {
		return "."
	}
	return "./" + strings.TrimPrefix(dir, "./")
}

func validatePackageTarget(target string) error {
	if target == "." {
		return nil
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(target)))
	if clean != "." {
		clean = "./" + strings.TrimPrefix(clean, "./")
	}
	if !strings.HasPrefix(target, "./") || strings.Contains(target, "\\") || strings.Contains(target, "..") || filepath.IsAbs(target) || clean != target {
		return fmt.Errorf("invalid package target %q", target)
	}
	for _, r := range target[2:] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '/' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid package target %q", target)
	}
	return nil
}

func isSafeDocumentation(path string) bool {
	base := filepath.Base(filepath.FromSlash(path))
	if base != "README.md" && base != "CHANGELOG.md" && !strings.HasPrefix(path, "docs/") {
		return false
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".md", ".markdown", ".rst", ".adoc":
		return true
	default:
		return false
	}
}
