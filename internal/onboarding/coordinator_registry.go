package onboarding

import (
	"context"
	"errors"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (c *Coordinator) verifyRegistryAuthority(request Request, receipt Receipt, committed bool) (registryAuthority, error) {
	if request.Airelay.SessionKey == nil || !request.Airelay.SessionRequired {
		return registryAuthority{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: errors.New("managed registry projection requires a required Airelay session key"),
		}
	}
	current, err := config.LoadManagedProjects(c.StateDir)
	if err != nil {
		return registryAuthority{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: fmt.Errorf("load managed project registry: %w", err),
		}
	}
	before, err := current.Digest()
	if err != nil {
		return registryAuthority{}, err
	}
	entry := config.ManagedProjectEntry{Root: request.Root, RepositoryURL: request.RepositoryURL, Remote: request.Remote, DefaultBranch: request.DefaultBranch, AirelaySessionKey: *request.Airelay.SessionKey}
	mirror := config.ManagedProjectMirrorPath(c.StateDir, request.ProjectID)
	for id, existing := range current.Projects {
		if id == request.ProjectID {
			if committed && before == receipt.RegistryDigests.ManagedAfterSHA256 && managedEntryEqual(existing, entry) {
				continue
			}
			return registryAuthority{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: fmt.Errorf("managed registry project ID collision: %s", request.ProjectID),
			}
		}
		if existing.Root == entry.Root || config.ManagedProjectMirrorPath(c.StateDir, id) == mirror || existing.AirelaySessionKey == entry.AirelaySessionKey || existing.RepositoryURL == entry.RepositoryURL {
			return registryAuthority{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: fmt.Errorf("managed registry collision with project %s", id),
			}
		}
	}

	if committed && before == receipt.RegistryDigests.ManagedAfterSHA256 {
		if existing, ok := current.Projects[request.ProjectID]; !ok || !managedEntryEqual(existing, entry) {
			return registryAuthority{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: errors.New("managed registry after digest does not contain the exact onboarding entry"),
			}
		}
		if _, err := config.EffectiveProjectsFromValidatedStatic(c.Hub.Config.Projects, current, c.StateDir); err != nil {
			return registryAuthority{}, &CoordinatorError{
				Code:  ErrOnboardingRecoveryRequired.Error(),
				Cause: err,
			}
		}
		return registryAuthority{
			Before: before,
			After:  before,
		}, nil
	}
	if before != receipt.RegistryDigests.ManagedBeforeSHA256 {
		return registryAuthority{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: fmt.Errorf("managed registry before digest mismatch: got %s want %s", before, receipt.RegistryDigests.ManagedBeforeSHA256),
		}
	}
	if current.Revision >= config.MaxManagedProjectRegistryRevision {
		return registryAuthority{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: errors.New("managed registry revision cannot advance"),
		}
	}
	next := cloneManagedRegistry(current)
	next.Revision++
	next.Projects[request.ProjectID] = entry
	if _, err := config.EffectiveProjectsFromValidatedStatic(c.Hub.Config.Projects, next, c.StateDir); err != nil {
		return registryAuthority{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: err,
		}
	}
	after, err := next.Digest()
	if err != nil {
		return registryAuthority{}, err
	}
	if after != receipt.RegistryDigests.ManagedAfterSHA256 {
		return registryAuthority{}, &CoordinatorError{
			Code:  ErrOnboardingRecoveryRequired.Error(),
			Cause: fmt.Errorf("managed registry after digest mismatch: got %s want %s", after, receipt.RegistryDigests.ManagedAfterSHA256),
		}
	}
	return registryAuthority{
		Before: before,
		After:  after,
	}, nil
}

func cloneManagedRegistry(current config.ManagedProjectRegistry) config.ManagedProjectRegistry {
	next := current
	next.Projects = make(map[string]config.ManagedProjectEntry, len(current.Projects)+1)
	for id, entry := range current.Projects {
		next.Projects[id] = entry
	}
	return next
}

func managedEntryEqual(left, right config.ManagedProjectEntry) bool {
	return left.Root == right.Root && left.RepositoryURL == right.RepositoryURL && left.Remote == right.Remote && left.DefaultBranch == right.DefaultBranch && left.AirelaySessionKey == right.AirelaySessionKey
}

func (c *Coordinator) remoteCollision(ctx context.Context, request Request) (bool, error) {
	projectPaths, err := c.Hub.List(ctx, "gpt-tunnel/v1/projects", "/project.json")
	if err != nil {
		return false, onboardingRecoveryError(err)
	}
	for _, path := range projectPaths {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			return false, onboardingRecoveryError(err)
		}
		var project model.Project
		if err := decodeStrictHubFile(data, &project); err != nil {
			return false, onboardingRecoveryError(err)
		}
		if err := model.ValidateProject(project); err != nil {
			return false, onboardingRecoveryError(err)
		}
		if project.ID == request.ProjectID && path != canonicalOnboardingPaths(request.ProjectID)[0] {
			return true, nil
		}
		if project.RepositoryURL == request.RepositoryURL && project.ID != request.ProjectID {
			return true, nil
		}
	}
	identifierPaths, err := c.Hub.List(ctx, "gpt-tunnel/v1/projects", "/identifiers.json")
	if err != nil {
		return false, onboardingRecoveryError(err)
	}
	for _, path := range identifierPaths {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			return false, onboardingRecoveryError(err)
		}
		var identifiers model.ProjectIdentifiers
		if err := decodeStrictHubFile(data, &identifiers); err != nil {
			return false, onboardingRecoveryError(err)
		}
		if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
			return false, onboardingRecoveryError(err)
		}
		if identifiers.ProjectID == request.ProjectID && path != canonicalOnboardingPaths(request.ProjectID)[2] {
			return true, nil
		}
		if identifiers.ProjectID != request.ProjectID && identifiers.ProjectCode == request.ProjectCode {
			return true, nil
		}
	}
	return false, nil
}
