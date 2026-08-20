package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func nextTrainV2ID(worktree, root, projectCode string) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(worktree, filepath.FromSlash(root)))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	var next uint64 = 1
	for _, entry := range entries {
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
			continue
		}
		code, number, err := model.ParseTrainV2ID(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil || code != projectCode {
			return "", fmt.Errorf("invalid train v2 member %q", entry.Name())
		}
		if number >= next {
			next = number + 1
		}
	}
	return model.FormatTrainV2ID(projectCode, next)
}
func canonicalTrainV2RecordName(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	_, _, err := model.ParseTrainV2ID(strings.TrimSuffix(name, ".json"))
	return err == nil
}
