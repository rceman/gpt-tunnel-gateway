package hub

import (
	"fmt"
	"os"
)

func RemoveFile(worktree, path string) (bool, error) {
	target, err := safeWritePath(worktree, path)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("hub removal target is not a regular file")
	}
	if err := os.Remove(target); err != nil {
		return false, err
	}
	return true, nil
}
