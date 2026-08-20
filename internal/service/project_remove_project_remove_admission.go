package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
