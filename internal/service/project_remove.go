package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

type ProjectRemoveInput struct {
	ProjectID string `json:"project_id"`
	WriteOptions
}

type ProjectRemoveResult struct {
	ProjectID         string                                    `json:"project_id"`
	AlreadyRemoved    bool                                      `json:"already_removed"`
	Hub               hub.TransactionResult                     `json:"hub,omitempty"`
	Registry          config.ManagedProjectRegistryWriteReceipt `json:"registry,omitempty"`
	ChangedPaths      []string                                  `json:"changed_paths"`
	LocalStateRemoved []string                                  `json:"local_state_removed"`
	ExternalRootKept  bool                                      `json:"external_root_kept"`
}

type projectRemovalSnapshot struct {
	BackupRoot      string
	Entries         []projectRemovalEntry
	RegistryBytes   []byte
	RegistryExisted bool
	ConfigBytes     []byte
	ConfigExisted   bool
	ConfigPath      string
}

type projectRemovalEntry struct {
	Original string
	Backup   string
}

type onboardingProjectState struct {
	ProjectID string `json:"project_id"`
	State     string `json:"state"`
}

func (s *Service) ProjectRemove(ctx context.Context, in ProjectRemoveInput) (ProjectRemoveResult, error) {
	if err := RequireWorkflowPolicyAuthority(ctx); err != nil {
		return ProjectRemoveResult{}, err
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return ProjectRemoveResult{}, err
	}

	project, projectErr := s.ProjectRead(ctx, in.ProjectID)
	managed, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		return ProjectRemoveResult{}, fmt.Errorf("load managed project registry: %w", err)
	}
	_, managedProject := managed.Projects[in.ProjectID]
	_, staticProject := s.Config.Projects[in.ProjectID]
	if IsNotFound(projectErr) && !managedProject && !staticProject {
		return ProjectRemoveResult{
			ProjectID:         in.ProjectID,
			AlreadyRemoved:    true,
			ChangedPaths:      []string{},
			LocalStateRemoved: []string{},
			ExternalRootKept:  true,
		}, nil
	}
	if projectErr != nil && !IsNotFound(projectErr) {
		return ProjectRemoveResult{}, projectErr
	}
	if !managedProject && !staticProject {
		return ProjectRemoveResult{}, projectErr
	}
	if projectErr == nil && (project.ID != in.ProjectID || project.Status != "active") {
		return ProjectRemoveResult{}, fmt.Errorf("project %q is not an active durable project", in.ProjectID)
	}

	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+in.ProjectID)
	if err != nil {
		return ProjectRemoveResult{}, fmt.Errorf("acquire project removal lock: %w", err)
	}
	defer projectLock.Release()
	managedLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "managed-projects")
	if err != nil {
		return ProjectRemoveResult{}, fmt.Errorf("acquire managed project lock: %w", err)
	}
	defer managedLock.Release()
	sessionLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "sessions")
	if err != nil {
		return ProjectRemoveResult{}, fmt.Errorf("acquire session lock: %w", err)
	}
	defer sessionLock.Release()

	if err := s.rejectLiveProjectAuthority(ctx, in.ProjectID); err != nil {
		return ProjectRemoveResult{}, err
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(in.ProjectID), "")
	if err != nil {
		return ProjectRemoveResult{}, fmt.Errorf("list project Hub subtree: %w", err)
	}
	snapshot, err := s.stageProjectLocalState(in.ProjectID, staticProject)
	if err != nil {
		return ProjectRemoveResult{}, err
	}
	rollback := func(cause error) (ProjectRemoveResult, error) {
		restoreErr := snapshot.restoreConfig()
		if restoreErr == nil {
			restoreErr = snapshot.restoreRegistry()
		}
		if restoreErr == nil {
			restoreErr = snapshot.restore()
		}
		if restoreErr != nil {
			return ProjectRemoveResult{}, fmt.Errorf("project removal failed: %w; rollback failed: %v", cause, restoreErr)
		}
		return ProjectRemoveResult{}, cause
	}

	registryDigest, err := managed.Digest()
	if err != nil {
		return rollback(fmt.Errorf("digest managed project registry: %w", err))
	}
	next := managed
	next.Projects = copyManagedProjects(managed.Projects)
	delete(next.Projects, in.ProjectID)
	next.Revision = managed.Revision + 1
	registryReceipt := config.ManagedProjectRegistryWriteReceipt{}
	if managedProject {
		registryReceipt, err = config.WriteManagedProjectRegistryLocked(s.Config.StateDir, registryDigest, next)
		if err != nil {
			return rollback(fmt.Errorf("remove project from managed registry: %w", err))
		}
	}
	if staticProject {
		if err := snapshot.removeConfigProject(in.ProjectID); err != nil {
			return rollback(fmt.Errorf("remove project from local configuration: %w", err))
		}
	}

	tx := hub.TransactionResult{}
	if len(paths) > 0 {
		tx, err = s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: remove project "+in.ProjectID, func(worktree string) ([]string, error) {
			var latest model.Project
			if err := readWorktreeJSON(worktree, s.projectPath(in.ProjectID), &latest); err != nil {
				return nil, fmt.Errorf("project changed before removal: %w", err)
			}
			if latest.ID != in.ProjectID || latest.Status != "active" {
				return nil, fmt.Errorf("project changed before removal")
			}
			if err := activeTrainInHubWorktree(worktree, in.ProjectID); err != nil {
				return nil, err
			}
			for _, path := range paths {
				if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(path))); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
			}
			return paths, nil
		})
		if err != nil {
			return rollback(err)
		}
	}
	if err := snapshot.commit(); err != nil {
		return ProjectRemoveResult{}, fmt.Errorf("project Hub removal committed but local cleanup could not complete: %w", err)
	}
	delete(s.Config.Projects, in.ProjectID)
	if bindings := s.Config.ProjectAgentBindings; bindings != nil {
		delete(bindings, in.ProjectID)
	}
	if bindings := s.Config.AgentBindings; bindings != nil {
		for key := range bindings {
			if key == in.ProjectID || strings.HasPrefix(key, in.ProjectID+"/") || strings.HasPrefix(key, in.ProjectID+"::") {
				delete(bindings, key)
			}
		}
	}
	return ProjectRemoveResult{
		ProjectID:         in.ProjectID,
		ChangedPaths:      append([]string{}, paths...),
		LocalStateRemoved: snapshot.originals(),
		ExternalRootKept:  true,
		Hub:               tx,
		Registry:          registryReceipt,
	}, nil
}

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
		if train.Status != model.TrainV2Completed && train.Status != model.TrainV2RecoveryQuarantined {
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
		if train.Status != model.TrainV2Completed && train.Status != model.TrainV2RecoveryQuarantined {
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
