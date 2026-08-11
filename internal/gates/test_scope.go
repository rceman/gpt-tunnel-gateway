package gates

import (
	"context"
	"fmt"
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
		return TestScope{Mode: TestScopeFull, FallbackReason: s.FallbackReason}, nil
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
		return TestScope{Mode: TestScopePackages, Packages: packages, FallbackReason: s.FallbackReason}, nil
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
	scope, err := resolveTestScope(ctx, root, changedFiles, discoverGoPackage)
	if err != nil {
		return TestScope{Mode: TestScopeFull, FallbackReason: err.Error()}, err
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return TestScope{Mode: TestScopeFull, FallbackReason: err.Error()}, err
	}
	return normalized, nil
}

type packageDiscovery func(context.Context, string, string) error

func resolveTestScope(ctx context.Context, root string, changedFiles []string, discover packageDiscovery) (TestScope, error) {
	if strings.TrimSpace(root) == "" {
		return FullTestScope(), fmt.Errorf("test scope root is empty")
	}
	if len(changedFiles) == 0 {
		return TestScope{Mode: TestScopeFull, FallbackReason: "changed file set is empty"}, fmt.Errorf("changed file set is empty")
	}
	packages := map[string]bool{}
	for _, path := range changedFiles {
		path = filepath.ToSlash(path)
		if err := model.ValidateRelativePath(path); err != nil {
			return FullTestScope(), fmt.Errorf("changed file %q is invalid: %w", path, err)
		}
		if strings.HasSuffix(path, ".go") {
			dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
			target := packageTarget(dir)
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
				if os.IsNotExist(err) {
					packages[target] = true
					continue
				}
				return FullTestScope(), fmt.Errorf("inspect Go package directory %q: %w", dir, err)
			}
			if err := discover(ctx, root, target); err != nil {
				return FullTestScope(), fmt.Errorf("discover Go package %q: %w", target, err)
			}
			packages[target] = true
			continue
		}
		if isSafeDocumentation(path) {
			continue
		}
		return FullTestScope(), fmt.Errorf("changed file %q has unknown test impact", path)
	}
	if len(packages) == 0 {
		return TestScope{Mode: TestScopePackages, Packages: []string{}}, nil
	}
	result := make([]string, 0, len(packages))
	for target := range packages {
		result = append(result, target)
	}
	sort.Strings(result)
	return TestScope{Mode: TestScopePackages, Packages: result}, nil
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
