package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func replaceAll(dir string, paths map[string]string, old map[string][]byte) error {
	staged := make(map[string]string, len(binaryOrder))
	cleanup := func() error {
		var first error
		for _, name := range binaryOrder {
			if path := staged[name]; path != "" {
				if err := stageRemove(path); err != nil && !os.IsNotExist(err) && first == nil {
					first = err
				}
			}
		}
		return first
	}
	for _, name := range binaryOrder {
		path, err := stageCopy(filepath.Join(dir, name), paths[name])
		if err != nil {
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("stage failed: %v; cleanup failed: %w", err, cleanErr)
			}
			return err
		}
		srcHash, hashErr := fileHash(filepath.Join(dir, name))
		stagedHash, stagedErr := fileHash(path)
		if hashErr != nil || stagedErr != nil || srcHash != stagedHash {
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("staged checksum verification failed; cleanup failed: %w", cleanErr)
			}
			return fmt.Errorf("staged checksum verification failed for %s", name)
		}
		if _, err := installedVersion(path); err != nil {
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("staged version verification failed; cleanup failed: %w", cleanErr)
			}
			return fmt.Errorf("staged version verification failed for %s", name)
		}
		staged[name] = path
	}
	for _, name := range binaryOrder {
		if err := stageRename(staged[name], paths[name]); err != nil {
			restoreErr := restoreAll(paths, old)
			cleanErr := cleanup()
			if restoreErr != nil {
				return fmt.Errorf("commit failed: %v; restore failed: %w", err, restoreErr)
			}
			if cleanErr != nil {
				return fmt.Errorf("commit failed: %v; staging cleanup failed: %w", err, cleanErr)
			}
			return err
		}
		staged[name] = ""
		if err := stageSyncDir(filepath.Dir(paths[name])); err != nil {
			restoreErr := restoreAll(paths, old)
			if restoreErr != nil {
				cleanErr := cleanup()
				if cleanErr != nil {
					return fmt.Errorf("commit directory sync failed: %v; restore failed: %w; cleanup failed: %v", err, restoreErr, cleanErr)
				}
				return fmt.Errorf("commit directory sync failed: %v; restore failed: %w", err, restoreErr)
			}
			if cleanErr := cleanup(); cleanErr != nil {
				return fmt.Errorf("commit directory sync failed: %v; cleanup failed: %w", err, cleanErr)
			}
			return err
		}
	}
	return cleanup()
}

func stageOne(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(in)
	closeInErr := in.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeInErr != nil {
		return "", closeInErr
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".gpt-tunnel-upgrade-stage-")
	if err != nil {
		return "", err
	}
	path := out.Name()
	cleanup := func(primary error) (string, error) {
		closeErr := out.Close()
		removeErr := stageRemove(path)
		if primary != nil {
			if closeErr != nil {
				primary = fmt.Errorf("%w; close failed: %v", primary, closeErr)
			}
			if removeErr != nil && !os.IsNotExist(removeErr) {
				primary = fmt.Errorf("%w; remove failed: %v", primary, removeErr)
			}
			return "", primary
		}
		if closeErr != nil {
			return "", closeErr
		}
		if removeErr != nil {
			return "", removeErr
		}
		return "", nil
	}
	if _, err := out.Write(data); err != nil {
		return cleanup(err)
	}
	if err := out.Chmod(0o755); err != nil {
		return cleanup(err)
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		return cleanup(err)
	}
	if _, err := os.Stat(path); err != nil {
		removeErr := stageRemove(path)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return "", fmt.Errorf("staged file stat failed: %v; remove failed: %w", err, removeErr)
		}
		return "", err
	}
	return path, nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func restoreAll(paths map[string]string, old map[string][]byte) error {
	var first error
	for _, name := range binaryOrder {
		dst := paths[name]
		if err := writeAtomicStrict(dst, old[name]); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func writeAtomicStrict(dst string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".gpt-tunnel-restore-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(primary error) error {
		closeErr := f.Close()
		removeErr := os.Remove(tmp)
		if primary != nil {
			if closeErr != nil {
				primary = fmt.Errorf("%w; close failed: %v", primary, closeErr)
			}
			if removeErr != nil && !os.IsNotExist(removeErr) {
				primary = fmt.Errorf("%w; remove failed: %v", primary, removeErr)
			}
			return primary
		}
		if closeErr != nil {
			return closeErr
		}
		return removeErr
	}
	if _, err = f.Write(data); err != nil {
		return cleanup(err)
	}
	if err = f.Chmod(0o755); err != nil {
		return cleanup(err)
	}
	if err = f.Sync(); err != nil {
		return cleanup(err)
	}
	if err = f.Close(); err != nil {
		removeErr := os.Remove(tmp)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("%w; remove failed: %v", err, removeErr)
		}
		return err
	}
	if err = os.Rename(tmp, dst); err != nil {
		removeErr := os.Remove(tmp)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("%w; remove failed: %v", err, removeErr)
		}
		return err
	}
	return syncDir(filepath.Dir(dst))
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}
func verifyHashes(paths map[string]string, expected map[string]string) error {
	for _, name := range binaryOrder {
		got, err := fileHash(paths[name])
		if err != nil || got != expected[name] {
			return fmt.Errorf("binary restoration checksum failed for %s", name)
		}
	}
	return nil
}
