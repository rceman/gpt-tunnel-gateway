package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func gatewayCompletionDestination(stateDir string, run model.Run) (string, error) {
	if run.CompletionPath == "" {
		return "", fmt.Errorf("run has no gateway-owned completion path")
	}
	expected, err := canonicalCompletionDestination(stateDir, run.ID)
	if err != nil {
		return "", err
	}
	destination, err := normalizedAbsolutePath(run.CompletionPath)
	if err != nil {
		return "", fmt.Errorf("invalid gateway completion path")
	}
	if destination != expected {
		return "", fmt.Errorf("gateway completion path must equal the canonical Run-specific path")
	}
	stateRoot, err := normalizedAbsolutePath(stateDir)
	if err != nil {
		return "", fmt.Errorf("invalid gateway state directory")
	}
	relative, err := filepath.Rel(stateRoot, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("gateway completion path escapes state directory")
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return "", err
	}
	if !parentInfo.IsDir() {
		return "", fmt.Errorf("gateway completion directory is not a directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("gateway completion directory cannot be resolved: %w", err)
	}
	resolvedParent, err = normalizedAbsolutePath(resolvedParent)
	if err != nil || resolvedParent != parent {
		return "", fmt.Errorf("gateway completion directory must not contain symlinks")
	}
	info, err := os.Lstat(destination)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("gateway completion path is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return destination, nil
}

type completionDirectory interface {
	Sync() error
	Close() error
}

var completionOpenDirectory = func(path string) (completionDirectory, error) {
	return os.Open(path)
}

func writeCompletionExclusive(path string, data []byte, task model.Task, maxReadBytes int64) (bool, error) {
	readExisting := func() (bool, error) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("gateway completion path is not a regular file")
		}
		existing, err := fsutil.ReadFileBounded(path, maxReadBytes)
		if err != nil {
			return false, err
		}
		if bytes.Equal(existing, data) {
			return true, nil
		}
		parsed, err := model.ParseCompletion(existing, task)
		if err == nil {
			canonical, canonicalErr := model.CompletionJSON(parsed)
			if canonicalErr == nil && bytes.Equal(append(canonical, '\n'), data) {
				return true, nil
			}
		}
		return false, fmt.Errorf("gateway completion already exists with different content")
	}
	if same, err := readExisting(); err != nil || same {
		return same, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".completion-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Link(temporaryPath, path); err == nil {
		directory, openErr := completionOpenDirectory(filepath.Dir(path))
		if openErr != nil {
			return false, fmt.Errorf("open completion directory for sync: %w", openErr)
		}
		if syncErr := directory.Sync(); syncErr != nil {
			_ = directory.Close()
			return false, fmt.Errorf("sync completion directory: %w", syncErr)
		}
		if closeErr := directory.Close(); closeErr != nil {
			return false, fmt.Errorf("close completion directory: %w", closeErr)
		}
		return false, nil
	} else if !errors.Is(err, os.ErrExist) {
		return false, err
	}
	if same, err := readExisting(); err != nil {
		return false, err
	} else if same {
		return true, nil
	}
	return false, fmt.Errorf("gateway completion appeared with different content")
}
