package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

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
	invalid.Projects["managed"] = ManagedProjectEntry{
		Root:              filepath.Join(stateDir, "missing"),
		RepositoryURL:     "git@github.com:example/managed.git",
		Remote:            "origin",
		DefaultBranch:     "main",
		AirelaySessionKey: "managed_master",
	}
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
