package config

import (
	"fmt"
	"path/filepath"
)

func EffectiveProjects(static map[string]ProjectConfig, managed ManagedProjectRegistry, stateDir string) (map[string]ProjectConfig, error) {
	if err := validateStateDir(stateDir); err != nil {
		return nil, err
	}
	stateDir = filepath.Clean(stateDir)
	managed, err := canonicalizeManagedProjectRegistry(managed)
	if err != nil {
		return nil, err
	}
	if err := managed.ValidateForStateDir(stateDir); err != nil {
		return nil, err
	}
	result := make(map[string]ProjectConfig, len(static)+len(managed.Projects))
	roots := map[string]string{}
	mirrors := map[string]string{}
	sessions := map[string]string{}
	ids := map[string]string{}
	for id, project := range static {
		root, mirror, err := validateStaticProject(id, project)
		if err != nil {
			return nil, err
		}
		if err := recordProjectCollision(id, root, mirror, project.AirelaySessionKey, ids, roots, mirrors, sessions); err != nil {
			return nil, err
		}
		result[id] = project
	}
	for id, entry := range managed.Projects {
		mirror := filepath.Clean(ManagedProjectMirrorPath(stateDir, id))
		if err := recordProjectCollision(id, entry.Root, mirror, entry.AirelaySessionKey, ids, roots, mirrors, sessions); err != nil {
			return nil, err
		}
		result[id] = ProjectConfig{
			Root:              entry.Root,
			Mirror:            mirror,
			Remote:            entry.Remote,
			DefaultBranch:     entry.DefaultBranch,
			ProjectCode:       entry.ProjectCode,
			AirelaySessionKey: entry.AirelaySessionKey,
			Watcher:           entry.Watcher,
		}
	}
	return result, nil
}

// EffectiveProjectsFromValidatedStatic combines static projects that were
// already validated and canonicalized by Config.Load with a dynamically
// validated managed registry. Static roots and mirrors are checked only for
// structural safety here; their current filesystem existence is intentionally
// not required because component operations own that check.
func EffectiveProjectsFromValidatedStatic(static map[string]ProjectConfig, managed ManagedProjectRegistry, stateDir string) (map[string]ProjectConfig, error) {
	managedProjects, err := EffectiveProjects(nil, managed, stateDir)
	if err != nil {
		return nil, err
	}
	result := make(map[string]ProjectConfig, len(static)+len(managedProjects))
	roots := map[string]string{}
	mirrors := map[string]string{}
	sessions := map[string]string{}
	ids := map[string]string{}
	for id, project := range static {
		if err := validateValidatedStaticProject(id, project); err != nil {
			return nil, err
		}
		if err := recordProjectCollision(id, project.Root, project.Mirror, project.AirelaySessionKey, ids, roots, mirrors, sessions); err != nil {
			return nil, err
		}
		result[id] = project
	}
	for id, project := range managedProjects {
		if err := recordProjectCollision(id, project.Root, project.Mirror, project.AirelaySessionKey, ids, roots, mirrors, sessions); err != nil {
			return nil, err
		}
		result[id] = project
	}
	return result, nil
}

func validateValidatedStaticProject(id string, project ProjectConfig) error {
	if err := validateManagedProjectID(id); err != nil {
		return err
	}
	if err := validateValidatedStaticPath(project.Root, "root"); err != nil {
		return err
	}
	if err := validateValidatedStaticPath(project.Mirror, "mirror"); err != nil {
		return err
	}
	if err := validateProjectValues(project.Remote, project.DefaultBranch, project.AirelaySessionKey); err != nil {
		return fmt.Errorf("invalid static project %q: %w", id, err)
	}
	return nil
}

func validateValidatedStaticPath(path, name string) error {
	if err := rejectUnsafeManagedValue(path, name); err != nil {
		return err
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("static project %s must be an absolute clean path", name)
	}
	return nil
}

func validateStaticProject(id string, project ProjectConfig) (string, string, error) {
	if err := validateManagedProjectID(id); err != nil {
		return "", "", err
	}
	root, err := canonicalDir(project.Root)
	if err != nil {
		return "", "", fmt.Errorf("invalid static project root %q: %w", id, err)
	}
	if err := validateProjectValues(project.Remote, project.DefaultBranch, project.AirelaySessionKey); err != nil {
		return "", "", fmt.Errorf("invalid static project %q: %w", id, err)
	}
	mirror, err := canonicalMirror(project.Mirror)
	if err != nil {
		return "", "", fmt.Errorf("invalid static project mirror %q: %w", id, err)
	}
	return root, mirror, nil
}

func recordProjectCollision(id, root, mirror, session string, ids, roots, mirrors, sessions map[string]string) error {
	if previous, ok := ids[id]; ok {
		return fmt.Errorf("duplicate project id %q from %s and %s", id, previous, id)
	}
	if previous, ok := roots[root]; ok {
		return fmt.Errorf("duplicate project root %q from %s and %s", root, previous, id)
	}
	if previous, ok := mirrors[mirror]; ok {
		return fmt.Errorf("duplicate project mirror %q from %s and %s", mirror, previous, id)
	}
	if previous, ok := sessions[session]; ok {
		return fmt.Errorf("duplicate project session %q from %s and %s", session, previous, id)
	}
	ids[id] = id
	roots[root] = id
	mirrors[mirror] = id
	sessions[session] = id
	return nil
}
