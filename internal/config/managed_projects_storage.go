package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func LoadManagedProjectRegistry(path string) (ManagedProjectRegistry, error) {
	return loadManagedProjectRegistry(path)
}

func LoadManagedProjects(stateDir string) (ManagedProjectRegistry, error) {
	return loadManagedProjectRegistry(ManagedProjectRegistryPath(stateDir))
}

func loadManagedProjectRegistry(path string) (ManagedProjectRegistry, error) {
	if path == "" {
		return ManagedProjectRegistry{}, fmt.Errorf("managed project registry path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EmptyManagedProjectRegistry(), nil
		}
		return ManagedProjectRegistry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ManagedProjectRegistry{}, fmt.Errorf("managed project registry must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return ManagedProjectRegistry{}, fmt.Errorf("managed project registry is not a regular file")
	}
	data, err := fsutil.ReadFileBounded(path, ManagedProjectRegistryMaxBytes)
	if err != nil {
		return ManagedProjectRegistry{}, err
	}
	var registry ManagedProjectRegistry
	if err := decodeManagedJSON(data, &registry); err != nil {
		return ManagedProjectRegistry{}, fmt.Errorf("decode managed project registry: %w", err)
	}
	registry, err = canonicalizeManagedProjectRegistry(registry)
	if err != nil {
		return ManagedProjectRegistry{}, err
	}
	if err := registry.ValidateForStateDir(filepath.Dir(path)); err != nil {
		return ManagedProjectRegistry{}, err
	}
	return registry, nil
}

func WriteManagedProjectRegistry(stateDir, expectedDigest string, next ManagedProjectRegistry) (ManagedProjectRegistryWriteReceipt, error) {
	if err := validateStateDir(stateDir); err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	stateDir = filepath.Clean(stateDir)
	lock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "managed-projects")
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	defer lock.Release()
	return WriteManagedProjectRegistryLocked(stateDir, expectedDigest, next)
}

// WriteManagedProjectRegistryLocked performs the registry transaction while
// the caller owns the canonical managed-projects lock. It re-reads the file,
// checks the expected digest/revision, and verifies the atomic replacement.
func WriteManagedProjectRegistryLocked(stateDir, expectedDigest string, next ManagedProjectRegistry) (ManagedProjectRegistryWriteReceipt, error) {
	if err := validateStateDir(stateDir); err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	stateDir = filepath.Clean(stateDir)
	path := ManagedProjectRegistryPath(stateDir)

	current, err := loadManagedProjectRegistry(path)
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	beforeDigest, err := current.Digest()
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if expectedDigest != beforeDigest {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("MANAGED_PROJECTS_DIGEST_CONFLICT expected=%s actual=%s", expectedDigest, beforeDigest)
	}
	if current.Revision >= MaxManagedProjectRegistryRevision {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("managed project registry revision cannot advance beyond safe integer maximum")
	}
	expectedRevision := current.Revision + 1
	if next.Revision != expectedRevision {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("managed project registry revision must be %d", expectedRevision)
	}
	next, err = canonicalizeManagedProjectRegistry(next)
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if err := next.ValidateForStateDir(stateDir); err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	afterDigest, err := next.Digest()
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if err := fsutil.WriteJSONAtomic(path, next, 0o600); err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	verified, err := loadManagedProjectRegistry(path)
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	verifiedDigest, err := verified.Digest()
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if verifiedDigest != afterDigest || verified.Revision != next.Revision {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("managed project registry verification failed")
	}
	return ManagedProjectRegistryWriteReceipt{
		Path:           path,
		BeforeDigest:   beforeDigest,
		AfterDigest:    afterDigest,
		BeforeRevision: current.Revision,
		AfterRevision:  next.Revision,
	}, nil
}

func UpdateManagedProjectRegistry(stateDir, expectedDigest string, next ManagedProjectRegistry) (ManagedProjectRegistryWriteReceipt, error) {
	return WriteManagedProjectRegistry(stateDir, expectedDigest, next)
}
