package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func copyManagedProjects(projects map[string]config.ManagedProjectEntry) map[string]config.ManagedProjectEntry {
	copy := make(map[string]config.ManagedProjectEntry, len(projects))
	for id, entry := range projects {
		copy[id] = entry
	}
	return copy
}
func (s *Service) rejectLiveProjectAuthority(ctx context.Context, projectID string) error {
	store := session.NewStore(s.Config.StateDir)
	records, err := store.List()
	if err != nil {
		return fmt.Errorf("inspect project sessions: %w", err)
	}
	for _, record := range records {
		if record.ProjectID == projectID && record.Status == session.StatusActive {
			return fmt.Errorf("PROJECT_REMOVE_ACTIVE_AUTHORITY: active session %s owns project %q", record.ID, projectID)
		}
	}
	if err := s.rejectActiveTrains(ctx, projectID); err != nil {
		return err
	}
	if err := rejectProjectOnboarding(s.Config.StateDir, projectID); err != nil {
		return err
	}
	if err := rejectProjectRuntime(s.Config.StateDir, projectID); err != nil {
		return err
	}
	return nil
}
func (s *Service) rejectActiveTrains(ctx context.Context, projectID string) error {
	paths, err := s.Hub.List(ctx, s.trainV2Root(projectID), ".json")
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspect project Train authority: %w", err)
	}
	for _, path := range paths {
		if !canonicalTrainV2RecordName(filepath.Base(path)) {
			continue
		}
		var train model.TrainV2
		if err := s.Hub.ReadJSON(ctx, path, &train); err != nil {
			return fmt.Errorf("inspect project Train authority: %w", err)
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return fmt.Errorf("inspect project Train authority: %w", err)
		}
		if train.Status != model.TrainV2Completed && train.Status != model.TrainV2RecoveryQuarantined && train.Status != model.TrainV2Retired {
			return fmt.Errorf("PROJECT_REMOVE_ACTIVE_AUTHORITY: Train %s is %s", train.ID, train.Status)
		}
		for _, item := range train.Items {
			for _, attempt := range item.Attempts {
				if attempt.Status == model.TrainV2AttemptRunning {
					return fmt.Errorf("PROJECT_REMOVE_ACTIVE_AUTHORITY: Train %s item %d Attempt %d is running", train.ID, item.Position, attempt.Number)
				}
			}
		}
	}
	return nil
}
func activeTrainInHubWorktree(worktree, projectID string) error {
	root := filepath.Join(worktree, filepath.FromSlash(hub.ProtocolRoot+"/projects/"+projectID+"/trains-v2"))
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect project Train authority in transaction: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
			continue
		}
		var train model.TrainV2
		path := filepath.ToSlash(filepath.Join(hub.ProtocolRoot, "projects", projectID, "trains-v2", entry.Name()))
		if err := readWorktreeJSON(worktree, path, &train); err != nil {
			return fmt.Errorf("inspect project Train authority in transaction: %w", err)
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return fmt.Errorf("inspect project Train authority in transaction: %w", err)
		}
		if train.Status != model.TrainV2Completed && train.Status != model.TrainV2RecoveryQuarantined && train.Status != model.TrainV2Retired {
			return fmt.Errorf("PROJECT_REMOVE_ACTIVE_AUTHORITY: Train %s changed to %s", train.ID, train.Status)
		}
	}
	return nil
}
func rejectProjectOnboarding(stateDir, projectID string) error {
	root := filepath.Join(stateDir, "onboarding")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect project onboarding authority: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			return fmt.Errorf("inspect project onboarding authority: %w", err)
		}
		var state onboardingProjectState
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("inspect project onboarding authority: %w", err)
		}
		if state.ProjectID == projectID && state.State != "activated" && state.State != "rolled_back" {
			return fmt.Errorf("PROJECT_REMOVE_ACTIVE_AUTHORITY: onboarding operation %s is %s", strings.TrimSuffix(entry.Name(), ".json"), state.State)
		}
	}
	return nil
}
func rejectProjectRuntime(stateDir, projectID string) error {
	supervisor, err := watcher.LoadSupervisor(stateDir, projectID)
	if err != nil {
		return fmt.Errorf("inspect project watcher authority: %w", err)
	}
	if supervisor.Desired != "stopped" || supervisor.Runtime != "stopped" {
		return fmt.Errorf("PROJECT_REMOVE_ACTIVE_AUTHORITY: watcher for project %q is desired=%s runtime=%s", projectID, supervisor.Desired, supervisor.Runtime)
	}
	runtimeRoot := filepath.Join(stateDir, "train-runtime", projectID)
	entries, err := os.ReadDir(runtimeRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect project runtime authority: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		binding, err := trainv2.ReadRuntime(stateDir, projectID, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return fmt.Errorf("inspect project runtime authority: %w", err)
		}
		if !binding.RestartRequired {
			return fmt.Errorf("PROJECT_REMOVE_ACTIVE_AUTHORITY: active runtime binding for Train %s", binding.TrainID)
		}
	}
	return nil
}
func (s *Service) stageProjectLocalState(projectID string, configured bool) (projectRemovalSnapshot, error) {
	paths := []string{
		filepath.Join(s.Config.StateDir, "git-mirrors", projectID+".git"),
		filepath.Join(s.Config.StateDir, "train-worktrees", projectID),
		filepath.Join(s.Config.StateDir, "train-runtime", projectID),
		filepath.Join(s.Config.StateDir, "train-attempts", projectID),
		filepath.Join(s.Config.StateDir, "gate-receipts", projectID),
		filepath.Join(s.Config.StateDir, "watchers", projectID),
	}
	store := session.NewStore(s.Config.StateDir)
	if records, err := store.List(); err == nil {
		for _, record := range records {
			if record.ProjectID == projectID && record.Status == session.StatusEnded {
				paths = append(paths, filepath.Join(s.Config.StateDir, "sessions", record.ID+".json"))
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return projectRemovalSnapshot{}, fmt.Errorf("inspect project local sessions: %w", err)
	}
	snapshot := projectRemovalSnapshot{}
	registryPath := config.ManagedProjectRegistryPath(s.Config.StateDir)
	if data, err := os.ReadFile(registryPath); err == nil {
		snapshot.RegistryBytes = append([]byte(nil), data...)
		snapshot.RegistryExisted = true
	} else if !os.IsNotExist(err) {
		return snapshot, fmt.Errorf("read managed project registry for rollback: %w", err)
	}
	if configured {
		if s.ConfigPath == "" {
			return snapshot, fmt.Errorf("configured project removal requires a durable config path")
		}
		data, err := os.ReadFile(s.ConfigPath)
		if err != nil {
			return snapshot, fmt.Errorf("read config for rollback: %w", err)
		}
		snapshot.ConfigPath = s.ConfigPath
		snapshot.ConfigBytes = append([]byte(nil), data...)
		snapshot.ConfigExisted = true
	}
	if err := os.MkdirAll(s.Config.StateDir, 0o700); err != nil {
		return snapshot, err
	}
	backup, err := os.MkdirTemp(s.Config.StateDir, ".project-remove-"+projectID+"-")
	if err != nil {
		return snapshot, err
	}
	snapshot.BackupRoot = backup
	seen := map[string]bool{}
	for _, path := range paths {
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			_ = snapshot.restore()
			return projectRemovalSnapshot{}, err
		}
		backupPath := filepath.Join(backup, fmt.Sprintf("%04d", len(snapshot.Entries)))
		if err := os.Rename(path, backupPath); err != nil {
			_ = snapshot.restore()
			return projectRemovalSnapshot{}, fmt.Errorf("stage local project state %q: %w", path, err)
		}
		snapshot.Entries = append(snapshot.Entries, projectRemovalEntry{
			Original: path,
			Backup:   backupPath,
		})
	}
	return snapshot, nil
}
