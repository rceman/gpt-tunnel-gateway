package service

import (
	"context"
	"fmt"
	"maps"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

// ProjectResolution is one immutable-in-memory snapshot of the configured
// project graph for a single service operation.
type ProjectResolution struct {
	Projects                map[string]config.ProjectConfig
	ManagedProjects         map[string]config.ManagedProjectEntry
	ManagedRegistryDigest   string
	ManagedRegistryRevision uint64
}

// resolveProjects loads and combines the static bootstrap projects with the
// current managed registry. It deliberately performs no caching or mutation so
// a successful registry transaction is visible on the next operation.
func (s *Service) resolveProjects() (ProjectResolution, error) {
	managed, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		return ProjectResolution{}, fmt.Errorf("load managed project registry: %w", err)
	}
	projects, err := config.EffectiveProjectsFromValidatedStatic(s.Config.Projects, managed, s.Config.StateDir)
	if err != nil {
		return ProjectResolution{}, fmt.Errorf("resolve effective projects: %w", err)
	}
	activeManaged := maps.Clone(managed.Projects)
	ctx := context.Background()
	if s.Durability != nil {
		localRetirements, err := s.Durability.LocalProjectRetirementIDs(ctx)
		if err != nil {
			return ProjectResolution{}, fmt.Errorf("load Local project retirements: %w", err)
		}
		for id := range localRetirements {
			delete(projects, id)
			delete(activeManaged, id)
		}
		sharedRetirements, err := s.Durability.ListSharedProjectRetirements(ctx)
		if err != nil {
			return ProjectResolution{}, fmt.Errorf("load Shared project retirements: %w", err)
		}
		for _, retirement := range sharedRetirements {
			delete(projects, retirement.ProjectID)
			delete(activeManaged, retirement.ProjectID)
		}
	}
	digest, err := managed.Digest()
	if err != nil {
		return ProjectResolution{}, fmt.Errorf("digest managed project registry: %w", err)
	}
	return ProjectResolution{
		Projects:                projects,
		ManagedProjects:         activeManaged,
		ManagedRegistryDigest:   digest,
		ManagedRegistryRevision: managed.Revision,
	}, nil
}

func (s *Service) effectiveProjectIDs() ([]string, ProjectResolution, error) {
	resolution, err := s.resolveProjects()
	if err != nil {
		return nil, ProjectResolution{}, err
	}
	ids := make([]string, 0, len(resolution.Projects))
	for id := range resolution.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, resolution, nil
}

// EffectiveProjectSnapshot returns one fresh static-plus-managed project
// snapshot for trusted internal callers.
func (s *Service) EffectiveProjectSnapshot() (ProjectResolution, error) {
	return s.resolveProjects()
}

// EffectiveProjectIDs returns sorted IDs from one fresh effective snapshot.
func (s *Service) EffectiveProjectIDs() ([]string, error) {
	ids, _, err := s.effectiveProjectIDs()
	return ids, err
}

// EffectiveProjectConfig resolves one project from the current effective
// snapshot without mutating the bootstrap configuration.
func (s *Service) EffectiveProjectConfig(id string) (config.ProjectConfig, error) {
	resolution, err := s.resolveProjects()
	if err != nil {
		return config.ProjectConfig{}, err
	}
	project, ok := resolution.Projects[id]
	if !ok {
		return config.ProjectConfig{}, fmt.Errorf("unknown local project %q", id)
	}
	return project, nil
}
