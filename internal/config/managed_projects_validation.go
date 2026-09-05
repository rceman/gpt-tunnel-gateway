package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func validateManagedProjectRegistry(registry ManagedProjectRegistry, stateDir string) error {
	if registry.SchemaVersion != ManagedProjectRegistrySchemaVersion {
		return fmt.Errorf("unsupported managed project registry schema_version")
	}
	if registry.Revision > MaxManagedProjectRegistryRevision {
		return fmt.Errorf("managed project registry revision exceeds safe integer maximum")
	}
	if registry.Projects == nil {
		return fmt.Errorf("managed project registry projects is required")
	}
	if len(registry.Projects) > MaxManagedProjectEntries {
		return fmt.Errorf("managed project registry exceeds %d entries", MaxManagedProjectEntries)
	}
	roots := map[string]string{}
	sessions := map[string]string{}
	mirrors := map[string]string{}
	for id, entry := range registry.Projects {
		if err := validateManagedProjectEntry(id, entry); err != nil {
			return err
		}
		if previous, ok := roots[entry.Root]; ok {
			return fmt.Errorf("duplicate managed project root %q from %s and %s", entry.Root, previous, id)
		}
		if previous, ok := sessions[entry.AirelaySessionKey]; ok {
			return fmt.Errorf("duplicate managed project session %q from %s and %s", entry.AirelaySessionKey, previous, id)
		}
		roots[entry.Root] = id
		sessions[entry.AirelaySessionKey] = id
		if stateDir != "" {
			mirror := filepath.Clean(ManagedProjectMirrorPath(stateDir, id))
			if previous, ok := mirrors[mirror]; ok {
				return fmt.Errorf("duplicate managed project mirror %q from %s and %s", mirror, previous, id)
			}
			mirrors[mirror] = id
		}
	}
	return nil
}

func validateManagedProjectEntry(id string, entry ManagedProjectEntry) error {
	if err := validateManagedProjectID(id); err != nil {
		return err
	}
	if err := rejectUnsafeManagedValue(entry.Root, "root"); err != nil {
		return err
	}
	if entry.Root == "" || !filepath.IsAbs(entry.Root) {
		return fmt.Errorf("managed project %q root must be absolute", id)
	}
	if _, err := canonicalDir(entry.Root); err != nil {
		return fmt.Errorf("invalid managed project root %q: %w", id, err)
	}
	if err := rejectUnsafeManagedValue(entry.RepositoryURL, "repository_url"); err != nil {
		return err
	}
	normalized, err := normalizeManagedRepositoryURL(entry.RepositoryURL)
	if err != nil {
		return fmt.Errorf("managed project %q: %w", id, err)
	}
	if normalized != entry.RepositoryURL {
		return fmt.Errorf("managed project %q repository_url is not normalized", id)
	}
	if err := validateProjectValues(entry.Remote, entry.DefaultBranch, entry.AirelaySessionKey); err != nil {
		return fmt.Errorf("invalid managed project %q: %w", id, err)
	}
	if entry.ProjectCode != "" && !managedProjectCodeRE.MatchString(entry.ProjectCode) {
		return fmt.Errorf("invalid managed project code %q", id)
	}
	return nil
}

func validateManagedProjectID(id string) error {
	if !managedProjectIDRE.MatchString(id) {
		return fmt.Errorf("invalid managed project identifier %q", id)
	}
	return nil
}

func validateProjectValues(remote, branch, session string) error {
	if err := rejectUnsafeManagedValue(remote, "remote"); err != nil {
		return err
	}
	if err := rejectUnsafeManagedValue(branch, "default_branch"); err != nil {
		return err
	}
	if err := rejectUnsafeManagedValue(session, "airelay_session_key"); err != nil {
		return err
	}
	if !managedRemoteRE.MatchString(remote) {
		return fmt.Errorf("invalid remote")
	}
	if err := validateBranch(branch); err != nil {
		return fmt.Errorf("invalid default_branch: %w", err)
	}
	if !managedSessionRE.MatchString(session) {
		return fmt.Errorf("invalid airelay_session_key")
	}
	return nil
}

func rejectUnsafeManagedValue(value, name string) error {
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s contains an unsafe control character", name)
	}
	return nil
}

func normalizeManagedRepositoryURL(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if err := validateRepositoryURL(normalized); err != nil {
		return "", err
	}
	if filepath.IsAbs(normalized) {
		normalized = filepath.Clean(normalized)
	}
	return normalized, nil
}

func canonicalMirror(path string) (string, error) {
	if err := rejectUnsafeManagedValue(path, "mirror"); err != nil {
		return "", err
	}
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("mirror must be absolute")
	}
	return filepath.Clean(path), nil
}

func validateStateDir(stateDir string) error {
	if stateDir == "" || !filepath.IsAbs(stateDir) || strings.ContainsAny(stateDir, "\x00\r\n") {
		return fmt.Errorf("state_dir must be an absolute safe path")
	}
	return nil
}

func canonicalizeManagedProjectRegistry(registry ManagedProjectRegistry) (ManagedProjectRegistry, error) {
	copy := registry
	if registry.Projects == nil {
		return ManagedProjectRegistry{}, fmt.Errorf("managed project registry projects is required")
	}
	copy.Projects = make(map[string]ManagedProjectEntry, len(registry.Projects))
	for id, entry := range registry.Projects {
		root, err := canonicalDir(entry.Root)
		if err != nil {
			return ManagedProjectRegistry{}, fmt.Errorf("invalid managed project root %q: %w", id, err)
		}
		entry.Root = root
		repositoryURL, err := normalizeManagedRepositoryURL(entry.RepositoryURL)
		if err != nil {
			return ManagedProjectRegistry{}, fmt.Errorf("managed project %q: %w", id, err)
		}
		entry.RepositoryURL = repositoryURL
		copy.Projects[id] = entry
	}
	return copy, nil
}
