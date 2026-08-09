package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
