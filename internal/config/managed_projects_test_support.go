package config

import (
	"os"
	"testing"
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
