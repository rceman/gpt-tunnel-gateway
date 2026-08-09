package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

func WriteJSON(worktree, path string, value any) error {
	target, err := safeWritePath(worktree, path)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(target, value, 0o600)
}
func WriteText(worktree, path, text string) error {
	target, err := safeWritePath(worktree, path)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(target, []byte(text), 0o600)
}
func safeWritePath(worktree, path string) (string, error) {
	if err := validateHubPath(path); err != nil {
		return "", err
	}
	root, err := filepath.Abs(worktree)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(filepath.ToSlash(filepath.Clean(path))))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("hub path escapes worktree")
	}
	current := root
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("hub path traverses symlink: %s", current)
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", statErr
		}
	}
	return target, nil
}
func validateHubPath(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) || strings.Contains(path, `\`) {
		return fmt.Errorf("invalid hub path")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	first := strings.Split(clean, "/")[0]
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.EqualFold(first, ".git") {
		return fmt.Errorf("hub path escapes root")
	}
	return nil
}
