package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectiveProjectsDerivesManagedMirrorsAndReturnsFreshMap(t *testing.T) {
	stateDir := t.TempDir()
	staticRoot := filepath.Join(stateDir, "static")
	managedRoot := filepath.Join(stateDir, "managed")
	if err := os.MkdirAll(staticRoot, 0o755); err != nil {
		t.Fatalf("create static root: %v", err)
	}
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	static := map[string]ProjectConfig{
		"static": {Root: staticRoot, Mirror: filepath.Join(stateDir, "static-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"},
	}
	managed := managedTestRegistry(managedRoot, "managed")
	projects, err := EffectiveProjects(static, managed, stateDir)
	if err != nil {
		t.Fatalf("effective projects: %v", err)
	}
	if got := projects["managed"].Mirror; got != ManagedProjectMirrorPath(stateDir, "managed") {
		t.Fatalf("managed mirror = %q, want derived mirror %q", got, ManagedProjectMirrorPath(stateDir, "managed"))
	}
	projects["static"] = ProjectConfig{}
	if static["static"].Root != staticRoot {
		t.Fatalf("effective project mutation changed static input")
	}
}

func TestEffectiveProjectsRejectsCrossSourceCollisions(t *testing.T) {
	stateDir := t.TempDir()
	rootA := filepath.Join(stateDir, "a")
	rootB := filepath.Join(stateDir, "b")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatalf("create root a: %v", err)
	}
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatalf("create root b: %v", err)
	}
	baseStatic := map[string]ProjectConfig{
		"static": {Root: rootA, Mirror: filepath.Join(stateDir, "static-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"},
	}
	tests := []struct {
		name    string
		static  map[string]ProjectConfig
		managed ManagedProjectRegistry
	}{
		{name: "project id", static: baseStatic, managed: managedTestRegistry(rootB, "static")},
		{name: "root", static: baseStatic, managed: managedTestRegistry(rootA, "managed")},
		{name: "mirror", static: map[string]ProjectConfig{"static": {Root: rootA, Mirror: ManagedProjectMirrorPath(stateDir, "managed"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"}}, managed: managedTestRegistry(rootB, "managed")},
		{name: "session", static: map[string]ProjectConfig{"static": {Root: rootA, Mirror: filepath.Join(stateDir, "static-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "managed_master"}}, managed: managedTestRegistry(rootB, "managed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := EffectiveProjects(test.static, test.managed, stateDir); err == nil {
				t.Fatalf("collision %q was accepted", test.name)
			}
		})
	}
}

func TestEffectiveProjectsFromValidatedStaticPreservesMissingStaticRoots(t *testing.T) {
	stateDir := t.TempDir()
	missingRoot := filepath.Join(stateDir, "deleted-static-root")
	static := map[string]ProjectConfig{
		"static": {Root: missingRoot, Mirror: filepath.Join(stateDir, "static-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"},
	}
	if _, err := EffectiveProjects(static, EmptyManagedProjectRegistry(), stateDir); err == nil {
		t.Fatal("strict EffectiveProjects accepted a missing arbitrary static root")
	}
	projects, err := EffectiveProjectsFromValidatedStatic(static, EmptyManagedProjectRegistry(), stateDir)
	if err != nil {
		t.Fatalf("trusted static merge rejected missing static root: %v", err)
	}
	if got := projects["static"].Root; got != missingRoot {
		t.Fatalf("trusted static root = %q, want %q", got, missingRoot)
	}

	managedMissing := managedTestRegistry(filepath.Join(stateDir, "deleted-managed-root"), "managed")
	if _, err := EffectiveProjectsFromValidatedStatic(nil, managedMissing, stateDir); err == nil {
		t.Fatal("trusted static merge accepted a missing managed root")
	}
}

func TestEffectiveProjectsFromValidatedStaticRejectsCrossSourceCollisions(t *testing.T) {
	stateDir := t.TempDir()
	managedRoot := filepath.Join(stateDir, "managed-root")
	if err := os.Mkdir(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	managedMirror := ManagedProjectMirrorPath(stateDir, "managed")
	tests := []struct {
		name   string
		static map[string]ProjectConfig
	}{
		{name: "id", static: map[string]ProjectConfig{"managed": {Root: filepath.Join(stateDir, "missing-id-root"), Mirror: filepath.Join(stateDir, "id-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"}}},
		{name: "root", static: map[string]ProjectConfig{"static": {Root: managedRoot, Mirror: filepath.Join(stateDir, "root-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"}}},
		{name: "mirror", static: map[string]ProjectConfig{"static": {Root: filepath.Join(stateDir, "missing-mirror-root"), Mirror: managedMirror, Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"}}},
		{name: "session", static: map[string]ProjectConfig{"static": {Root: filepath.Join(stateDir, "missing-session-root"), Mirror: filepath.Join(stateDir, "session-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "managed_master"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := EffectiveProjectsFromValidatedStatic(test.static, managedTestRegistry(managedRoot, "managed"), stateDir); err == nil || !strings.Contains(err.Error(), "duplicate project") {
				t.Fatalf("collision error = %v", err)
			}
		})
	}
}

func TestEffectiveProjectsFromValidatedStaticRejectsUnsafeStaticStructureWithoutLookup(t *testing.T) {
	stateDir := t.TempDir()
	base := func() ProjectConfig {
		return ProjectConfig{
			Root:              filepath.Join(stateDir, "missing-root"),
			Mirror:            filepath.Join(stateDir, "mirror.git"),
			Remote:            "origin",
			DefaultBranch:     "main",
			AirelaySessionKey: "static_master",
		}
	}
	tests := []struct {
		name    string
		project ProjectConfig
		id      string
	}{
		{name: "invalid id", project: base(), id: "INVALID"},
		{name: "relative root", project: func() ProjectConfig { p := base(); p.Root = "relative"; return p }(), id: "static"},
		{name: "unclean root", project: func() ProjectConfig { p := base(); p.Root = stateDir + "/missing/../root"; return p }(), id: "static"},
		{name: "relative mirror", project: func() ProjectConfig { p := base(); p.Mirror = "relative-mirror.git"; return p }(), id: "static"},
		{name: "unsafe remote", project: func() ProjectConfig { p := base(); p.Remote = "origin\n"; return p }(), id: "static"},
		{name: "unsafe branch", project: func() ProjectConfig { p := base(); p.DefaultBranch = "main\n"; return p }(), id: "static"},
		{name: "unsafe session", project: func() ProjectConfig { p := base(); p.AirelaySessionKey = "session\n"; return p }(), id: "static"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := EffectiveProjectsFromValidatedStatic(map[string]ProjectConfig{test.id: test.project}, EmptyManagedProjectRegistry(), stateDir); err == nil {
				t.Fatal("unsafe static project unexpectedly accepted")
			}
		})
	}
}

func TestEffectiveProjectsFromValidatedStaticReturnsFreshInputs(t *testing.T) {
	stateDir := t.TempDir()
	staticRoot := filepath.Join(stateDir, "static-root")
	managedRoot := filepath.Join(stateDir, "managed-root")
	if err := os.MkdirAll(staticRoot, 0o755); err != nil {
		t.Fatalf("create static root: %v", err)
	}
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	static := map[string]ProjectConfig{
		"static": {Root: staticRoot, Mirror: filepath.Join(stateDir, "static-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"},
	}
	managed := managedTestRegistry(managedRoot, "managed")
	projects, err := EffectiveProjectsFromValidatedStatic(static, managed, stateDir)
	if err != nil {
		t.Fatalf("trusted static merge: %v", err)
	}
	projects["static"] = ProjectConfig{}
	delete(projects, "managed")
	if static["static"].Root != staticRoot {
		t.Fatalf("result mutation changed static input")
	}
	if managed.Projects["managed"].Root != managedRoot {
		t.Fatalf("result mutation changed managed input")
	}
}

func TestEffectiveProjectsFromValidatedStaticAbsentRegistryHasNoReadSideEffects(t *testing.T) {
	stateDir := t.TempDir()
	if _, err := LoadManagedProjects(stateDir); err != nil {
		t.Fatalf("load absent registry: %v", err)
	}
	if _, err := EffectiveProjectsFromValidatedStatic(nil, EmptyManagedProjectRegistry(), stateDir); err != nil {
		t.Fatalf("merge absent registry: %v", err)
	}
	for _, path := range []string{
		ManagedProjectRegistryPath(stateDir),
		filepath.Join(stateDir, "locks"),
		filepath.Join(stateDir, "git-mirrors"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("read/merge created %q, stat error=%v", path, err)
		}
	}
}
