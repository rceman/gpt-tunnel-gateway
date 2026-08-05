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

func TestManagedProjectRegistryRejectsNullOmittedAndNilProjects(t *testing.T) {
	stateDir := t.TempDir()
	for name, projects := range map[string]string{
		"null":    "null",
		"omitted": "",
	} {
		path := filepath.Join(stateDir, name+".json")
		data := `{"schema_version":1,"revision":0}`
		if projects != "" {
			data = `{"schema_version":1,"revision":0,"projects":` + projects + `}`
		}
		writeManagedTestFile(t, path, []byte(data))
		requireManagedLoadError(t, path)
	}

	nilRegistry := ManagedProjectRegistry{SchemaVersion: ManagedProjectRegistrySchemaVersion}
	if err := nilRegistry.Validate(); err == nil {
		t.Fatalf("nil projects unexpectedly validated")
	}
	if _, err := nilRegistry.CanonicalJSON(); err == nil {
		t.Fatalf("nil projects unexpectedly canonicalized")
	}
	if _, err := nilRegistry.Digest(); err == nil {
		t.Fatalf("nil projects unexpectedly digested")
	}
	if _, err := EffectiveProjects(nil, nilRegistry, stateDir); err == nil {
		t.Fatalf("nil projects unexpectedly resolved")
	}
}

func TestManagedProjectRegistryRevisionSafeIntegerJSONParity(t *testing.T) {
	stateDir := t.TempDir()
	valid := []string{
		"0",
		"0.0",
		"0e0",
		"1",
		"1.0",
		"1e0",
		"9007199254740991",
		"9007199254740991.0",
		"9007199254740991e0",
	}
	var canonical []byte
	for index, number := range valid {
		path := filepath.Join(stateDir, "valid-"+string(rune('a'+index))+".json")
		writeManagedTestFile(t, path, []byte(`{"schema_version":1,"revision":`+number+`,"projects":{}}`))
		registry, err := LoadManagedProjectRegistry(path)
		if err != nil {
			t.Fatalf("load valid revision %s: %v", number, err)
		}
		want := uint64(0)
		if strings.HasPrefix(number, "1") {
			want = 1
		}
		if strings.HasPrefix(number, "9") {
			want = MaxManagedProjectRegistryRevision
		}
		if registry.Revision != want {
			t.Fatalf("revision %s decoded as %d, want %d", number, registry.Revision, want)
		}
		encoded, err := registry.CanonicalJSON()
		if err != nil {
			t.Fatalf("canonicalize revision %s: %v", number, err)
		}
		if canonical == nil {
			canonical = encoded
		} else if !strings.Contains(string(encoded), `"revision":`+strings.TrimSuffix(strings.TrimSuffix(number, ".0"), "e0")) && strings.HasPrefix(number, "1") {
			t.Fatalf("canonical revision lost integer value for %s: %s", number, encoded)
		}
	}

	invalid := []string{"true", "null", "-0", "-1", "1.5", "1e-1", "1e-999999", "9007199254740992", "9007199254740992.0", "1e999999"}
	for index, number := range invalid {
		path := filepath.Join(stateDir, "invalid-"+string(rune('a'+index))+".json")
		writeManagedTestFile(t, path, []byte(`{"schema_version":1,"revision":`+number+`,"projects":{}}`))
		requireManagedLoadError(t, path)
	}

	programmatic := EmptyManagedProjectRegistry()
	programmatic.Revision = MaxManagedProjectRegistryRevision + 1
	if err := programmatic.Validate(); err == nil {
		t.Fatalf("programmatic revision overflow unexpectedly validated")
	}
	if _, err := programmatic.Digest(); err == nil {
		t.Fatalf("programmatic revision overflow unexpectedly digested")
	}
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

func TestManagedProjectRegistryWriterRejectsPersistedMaximumRevisionWithoutMutation(t *testing.T) {
	stateDir := t.TempDir()
	path := ManagedProjectRegistryPath(stateDir)
	persisted := EmptyManagedProjectRegistry()
	persisted.Revision = MaxManagedProjectRegistryRevision
	data, err := persisted.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonicalize maximum revision registry: %v", err)
	}
	writeManagedTestFile(t, path, data)
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read maximum revision registry: %v", err)
	}
	loaded, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("load maximum revision registry: %v", err)
	}
	beforeDigest, err := loaded.Digest()
	if err != nil {
		t.Fatalf("digest maximum revision registry: %v", err)
	}
	if loaded.Revision != MaxManagedProjectRegistryRevision {
		t.Fatalf("loaded revision = %d, want %d", loaded.Revision, MaxManagedProjectRegistryRevision)
	}

	candidate := EmptyManagedProjectRegistry()
	if _, err := WriteManagedProjectRegistry(stateDir, beforeDigest, candidate); err == nil || !strings.Contains(err.Error(), "cannot advance beyond safe integer maximum") {
		t.Fatalf("maximum revision write error = %v, want safe-integer advance rejection", err)
	}

	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read registry after maximum revision rejection: %v", err)
	}
	if string(afterBytes) != string(beforeBytes) {
		t.Fatalf("maximum revision rejection changed persisted bytes")
	}
	after, err := LoadManagedProjectRegistry(path)
	if err != nil {
		t.Fatalf("reload maximum revision registry: %v", err)
	}
	afterDigest, err := after.Digest()
	if err != nil {
		t.Fatalf("digest registry after maximum revision rejection: %v", err)
	}
	if afterDigest != beforeDigest {
		t.Fatalf("maximum revision rejection changed digest: %q versus %q", afterDigest, beforeDigest)
	}
	if after.Revision != MaxManagedProjectRegistryRevision {
		t.Fatalf("revision after rejection = %d, want %d", after.Revision, MaxManagedProjectRegistryRevision)
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
		return ProjectConfig{Root: filepath.Join(stateDir, "missing-root"), Mirror: filepath.Join(stateDir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "static_master"}
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
