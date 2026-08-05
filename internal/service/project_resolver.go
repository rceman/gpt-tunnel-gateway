package service

import (
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

// ProjectResolution is one immutable-in-memory snapshot of the configured
// project graph for a single service operation.
type ProjectResolution struct {
	Projects                map[string]config.ProjectConfig
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
	projects, err := config.EffectiveProjects(s.Config.Projects, managed, s.Config.StateDir)
	if err != nil {
		return ProjectResolution{}, fmt.Errorf("resolve effective projects: %w", err)
	}
	digest, err := managed.Digest()
	if err != nil {
		return ProjectResolution{}, fmt.Errorf("digest managed project registry: %w", err)
	}
	return ProjectResolution{
		Projects:                projects,
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
