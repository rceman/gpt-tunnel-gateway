package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type BackupResult struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Ref    string `json:"ref"`
}

// Backup creates a recoverable local bundle of the configured authoritative
// branch without changing the managed checkout or remote branch.
func (s Store) Backup(ctx context.Context, prefix string) (BackupResult, error) {
	lock, err := s.readOnlyLock()
	if err != nil {
		return BackupResult{}, err
	}
	defer lock.Release()
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		return BackupResult{}, err
	}
	dir := filepath.Join(s.Config.StateDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupResult{}, err
	}
	name := fmt.Sprintf("%s-%s.bundle", prefix, time.Now().UTC().Format("20060102T150405Z"))
	path := filepath.Join(dir, name)
	if _, err := command(ctx, root, "bundle", "create", path, s.remoteRef()); err != nil {
		return BackupResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return BackupResult{}, err
	}
	sum := sha256.Sum256(data)
	if err := os.Chmod(path, 0o600); err != nil {
		return BackupResult{}, err
	}
	return BackupResult{
		Path:   path,
		SHA256: hex.EncodeToString(sum[:]),
		Ref:    s.remoteRef(),
	}, nil
}
