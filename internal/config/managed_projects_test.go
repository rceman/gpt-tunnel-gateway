package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func managedTestEntry(root, projectID string) ManagedProjectEntry {
	return ManagedProjectEntry{
		Root:              root,
		RepositoryURL:     "git@github.com:example/" + projectID + ".git",
		Remote:            "origin",
		DefaultBranch:     "main",
		AirelaySessionKey: projectID + "_master",
	}
}

func managedTestRegistry(root, projectID string) ManagedProjectRegistry {
	return ManagedProjectRegistry{
		SchemaVersion: ManagedProjectRegistrySchemaVersion,
		Revision:      0,
		Projects: map[string]ManagedProjectEntry{
			projectID: managedTestEntry(root, projectID),
		},
	}
}

func writeManagedTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write registry fixture: %v", err)
	}
}

func requireManagedLoadError(t *testing.T, path string) {
	t.Helper()
	if _, err := LoadManagedProjectRegistry(path); err == nil {
		t.Fatalf("LoadManagedProjectRegistry(%q) unexpectedly succeeded", path)
	}
}

func TestManagedProjectRegistryAbsentDigestAndPaths(t *testing.T) {
	stateDir := t.TempDir()
	path := ManagedProjectRegistryPath(stateDir)
	if want := filepath.Join(stateDir, "managed-projects.json"); path != want {
		t.Fatalf("registry path = %q, want %q", path, want)
	}
	if want := filepath.Join(stateDir, "git-mirrors", "demo.git"); ManagedProjectMirrorPath(stateDir, "demo") != want {
		t.Fatalf("mirror path = %q, want %q", ManagedProjectMirrorPath(stateDir, "demo"), want)
	}

	first, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("load absent registry: %v", err)
	}
	second, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("load absent registry again: %v", err)
	}
	if first.SchemaVersion != 1 || first.Revision != 0 || len(first.Projects) != 0 {
		t.Fatalf("unexpected empty registry: %#v", first)
	}
	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatalf("digest first empty registry: %v", err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatalf("digest second empty registry: %v", err)
	}
	if firstDigest != secondDigest || firstDigest == "" {
		t.Fatalf("empty registry digest is not stable: %q versus %q", firstDigest, secondDigest)
	}
}

func TestManagedProjectRegistryStrictDecode(t *testing.T) {
	stateDir := t.TempDir()
	valid := []byte(`{"schema_version":1,"revision":0,"projects":{}}`)

	tests := []struct {
		name string
		data []byte
	}{
		{name: "unknown field", data: []byte(`{"schema_version":1,"revision":0,"projects":{},"unknown":true}`)},
		{name: "duplicate field", data: []byte(`{"schema_version":1,"revision":0,"projects":{},"revision":0}`)},
		{name: "trailing value", data: append(append([]byte(nil), valid...), []byte(`{}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(stateDir, test.name+".json")
			writeManagedTestFile(t, path, test.data)
			requireManagedLoadError(t, path)
		})
	}

	oversized := filepath.Join(stateDir, "oversized.json")
	writeManagedTestFile(t, oversized, []byte(strings.Repeat("x", ManagedProjectRegistryMaxBytes+1)))
	requireManagedLoadError(t, oversized)

	target := filepath.Join(stateDir, "target.json")
	writeManagedTestFile(t, target, valid)
	symlink := filepath.Join(stateDir, "symlink.json")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatalf("create registry symlink: %v", err)
	}
	requireManagedLoadError(t, symlink)
}

func TestManagedProjectRegistryCanonicalizesRootsAndRejectsInvalidValues(t *testing.T) {
	stateDir := t.TempDir()
	root := filepath.Join(stateDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create root: %v", err)
	}
	rootLink := filepath.Join(stateDir, "root-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatalf("create root symlink: %v", err)
	}

	registry := managedTestRegistry(rootLink, "demo")
	data, err := registry.CanonicalJSON()
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	path := ManagedProjectRegistryPath(stateDir)
	writeManagedTestFile(t, path, data)
	loaded, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("load canonicalizable registry: %v", err)
	}
	if loaded.Projects["demo"].Root != root {
		t.Fatalf("root = %q, want canonical %q", loaded.Projects["demo"].Root, root)
	}

	invalid := []ManagedProjectRegistry{
		{SchemaVersion: 2, Projects: map[string]ManagedProjectEntry{}},
		{SchemaVersion: 1, Projects: map[string]ManagedProjectEntry{"bad id!": managedTestEntry(root, "bad")}},
		{SchemaVersion: 1, Projects: map[string]ManagedProjectEntry{"demo": {Root: root, RepositoryURL: "git@github.com:example/demo.git\n", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "demo_master"}}},
	}
	for index, candidate := range invalid {
		if err := candidate.ValidateForStateDir(stateDir); err == nil {
			t.Fatalf("invalid registry %d unexpectedly validated", index)
		}
	}
}

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

func TestManagedProjectRegistryWriterUsesDigestRevisionAndAtomicState(t *testing.T) {
	stateDir := t.TempDir()
	path := ManagedProjectRegistryPath(stateDir)
	current := EmptyManagedProjectRegistry()
	expectedDigest, err := current.Digest()
	if err != nil {
		t.Fatalf("empty digest: %v", err)
	}
	root := filepath.Join(stateDir, "managed")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	next := managedTestRegistry(root, "managed")
	next.Revision = 1
	receipt, err := WriteManagedProjectRegistry(stateDir, expectedDigest, next)
	if err != nil {
		t.Fatalf("write managed registry: %v", err)
	}
	if receipt.BeforeDigest != expectedDigest || receipt.AfterDigest == expectedDigest || receipt.BeforeRevision != 0 || receipt.AfterRevision != 1 {
		t.Fatalf("unexpected write receipt: %#v", receipt)
	}
	if got := receipt.Path; got != path {
		t.Fatalf("receipt path = %q, want %q", got, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat registry: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("registry mode = %o, want 600", info.Mode().Perm())
	}
	loaded, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	loadedDigest, err := loaded.Digest()
	if err != nil {
		t.Fatalf("reload digest: %v", err)
	}
	if loadedDigest != receipt.AfterDigest {
		t.Fatalf("reloaded digest = %q, receipt digest = %q", loadedDigest, receipt.AfterDigest)
	}

	staleCandidate := next
	staleCandidate.Revision = 2
	if _, err := WriteManagedProjectRegistry(stateDir, "stale", staleCandidate); err == nil {
		t.Fatalf("stale digest write unexpectedly succeeded")
	}
	unchanged, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("load after stale rejection: %v", err)
	}
	unchangedDigest, err := unchanged.Digest()
	if err != nil {
		t.Fatalf("digest after stale rejection: %v", err)
	}
	if unchangedDigest != receipt.AfterDigest {
		t.Fatalf("stale rejection changed registry: %q versus %q", unchangedDigest, receipt.AfterDigest)
	}

	jump := next
	jump.Revision = 3
	if _, err := WriteManagedProjectRegistry(stateDir, receipt.AfterDigest, jump); err == nil {
		t.Fatalf("revision jump write unexpectedly succeeded")
	}
	invalid := next
	invalid.Revision = 2
	invalid.Projects["managed"] = ManagedProjectEntry{Root: filepath.Join(stateDir, "missing"), RepositoryURL: "git@github.com:example/managed.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "managed_master"}
	if _, err := WriteManagedProjectRegistry(stateDir, receipt.AfterDigest, invalid); err == nil {
		t.Fatalf("invalid registry write unexpectedly succeeded")
	}
	final, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("load after rejected writes: %v", err)
	}
	finalDigest, err := final.Digest()
	if err != nil {
		t.Fatalf("final digest: %v", err)
	}
	if finalDigest != receipt.AfterDigest || final.Revision != 1 {
		t.Fatalf("rejected writes changed state: digest=%q revision=%d", finalDigest, final.Revision)
	}
}

func TestManagedProjectRegistryWriterRejectsBusyLock(t *testing.T) {
	stateDir := t.TempDir()
	current := EmptyManagedProjectRegistry()
	expectedDigest, err := current.Digest()
	if err != nil {
		t.Fatalf("empty digest: %v", err)
	}
	lease, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "managed-projects")
	if err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	root := filepath.Join(stateDir, "managed")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	next := managedTestRegistry(root, "managed")
	next.Revision = 1
	if _, err := WriteManagedProjectRegistry(stateDir, expectedDigest, next); err == nil {
		t.Fatalf("busy-lock write unexpectedly succeeded")
	}
	if _, err := os.Stat(ManagedProjectRegistryPath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("busy-lock write changed registry path, stat error=%v", err)
	}
}

func TestManagedProjectRegistryNestedDuplicateAndTrailingFieldsRejected(t *testing.T) {
	stateDir := t.TempDir()
	root := filepath.Join(stateDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create root: %v", err)
	}
	entry := `{"root":"` + root + `","repository_url":"git@github.com:example/demo.git","remote":"origin","default_branch":"main","airelay_session_key":"demo_master"}`
	for name, data := range map[string]string{
		"duplicate_entry_field": `{"schema_version":1,"revision":0,"projects":{"demo":` + strings.Replace(entry, `"remote":"origin"`, `"remote":"origin","remote":"origin"`, 1) + `}}`,
		"unknown_entry_field":   `{"schema_version":1,"revision":0,"projects":{"demo":` + strings.TrimSuffix(entry, "}") + `,"unknown":true}}`,
		"entry_trailing":        `{"schema_version":1,"revision":0,"projects":{"demo":` + strings.TrimSuffix(entry, "}") + `}{}}`,
	} {
		path := filepath.Join(stateDir, name+".json")
		writeManagedTestFile(t, path, []byte(data))
		requireManagedLoadError(t, path)
	}

	// Keep encoding/json in the test dependency set tied to the strict JSON
	// fixtures rather than relying on hand-written escaping for all cases.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(`{"schema_version":1}`), &decoded); err != nil {
		t.Fatalf("sanity JSON fixture: %v", err)
	}
}
