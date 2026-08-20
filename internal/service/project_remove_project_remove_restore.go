package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

func (s projectRemovalSnapshot) originals() []string {
	result := make([]string, 0, len(s.Entries))
	for _, entry := range s.Entries {
		result = append(result, entry.Original)
	}
	sort.Strings(result)
	return result
}
func (s projectRemovalSnapshot) restore() error {
	var first error
	for i := len(s.Entries) - 1; i >= 0; i-- {
		entry := s.Entries[i]
		if _, err := os.Lstat(entry.Backup); err != nil {
			if !os.IsNotExist(err) && first == nil {
				first = err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(entry.Original), 0o700); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if err := os.Rename(entry.Backup, entry.Original); err != nil && first == nil {
			first = err
		}
	}
	if err := os.RemoveAll(s.BackupRoot); err != nil && first == nil {
		first = err
	}
	return first
}
func (s projectRemovalSnapshot) commit() error { return os.RemoveAll(s.BackupRoot) }
func (s projectRemovalSnapshot) restoreRegistry() error {
	path := config.ManagedProjectRegistryPath(filepath.Dir(s.BackupRoot))
	if !s.RegistryExisted {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return fsutil.WriteFileAtomic(path, s.RegistryBytes, 0o600)
}
func (s projectRemovalSnapshot) restoreConfig() error {
	if !s.ConfigExisted {
		return nil
	}
	return fsutil.WriteFileAtomic(s.ConfigPath, s.ConfigBytes, 0o600)
}
func (s projectRemovalSnapshot) removeConfigProject(projectID string) error {
	data, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	projects, ok := root["projects"].(map[string]any)
	if !ok {
		return errors.New("config projects is not an object")
	}
	delete(projects, projectID)
	if bindings, ok := root["project_agent_bindings"].(map[string]any); ok {
		delete(bindings, projectID)
	}
	if bindings, ok := root["agent_bindings"].(map[string]any); ok {
		for key := range bindings {
			if key == projectID || strings.HasPrefix(key, projectID+"/") || strings.HasPrefix(key, projectID+"::") {
				delete(bindings, key)
			}
		}
	}
	updated, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	if err := fsutil.WriteFileAtomic(s.ConfigPath, updated, 0o600); err != nil {
		return err
	}
	return nil
}
