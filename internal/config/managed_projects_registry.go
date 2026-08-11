package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
)

const (
	ManagedProjectRegistrySchemaVersion = 1
	ManagedProjectRegistryMaxBytes      = 1 << 20
	MaxManagedProjectEntries            = 256
	MaxManagedProjectRegistryRevision   = uint64(9007199254740991)
)

var (
	managedProjectIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	managedRemoteRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	managedSessionRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// ManagedProjectRegistry is the local, gateway-owned project registry.
// Projects are keyed by the gateway project identifier; mirrors are derived
// from the state directory and are deliberately not caller-controlled.
type ManagedProjectRegistry struct {
	SchemaVersion int                            `json:"schema_version"`
	Revision      uint64                         `json:"revision"`
	Projects      map[string]ManagedProjectEntry `json:"projects"`
}

type ManagedProjectEntry struct {
	Root              string          `json:"root"`
	RepositoryURL     string          `json:"repository_url"`
	Remote            string          `json:"remote"`
	DefaultBranch     string          `json:"default_branch"`
	AirelaySessionKey string          `json:"airelay_session_key"`
	Watcher           WatcherSettings `json:"watcher,omitempty"`
}

type ManagedProjectRegistryWriteReceipt struct {
	Path           string `json:"path"`
	BeforeDigest   string `json:"before_digest"`
	AfterDigest    string `json:"after_digest"`
	BeforeRevision uint64 `json:"before_revision"`
	AfterRevision  uint64 `json:"after_revision"`
}

type ManagedProjectRegistryWriteResult = ManagedProjectRegistryWriteReceipt

func ManagedProjectRegistryPath(stateDir string) string {
	return filepath.Join(filepath.Clean(stateDir), "managed-projects.json")
}

func ManagedProjectMirrorPath(stateDir, projectID string) string {
	return filepath.Join(filepath.Clean(stateDir), "git-mirrors", projectID+".git")
}

func EmptyManagedProjectRegistry() ManagedProjectRegistry {
	return ManagedProjectRegistry{
		SchemaVersion: ManagedProjectRegistrySchemaVersion,
		Projects:      map[string]ManagedProjectEntry{},
	}
}

func (r ManagedProjectRegistry) Validate() error {
	return validateManagedProjectRegistry(r, "")
}

func (r ManagedProjectRegistry) ValidateForStateDir(stateDir string) error {
	return validateManagedProjectRegistry(r, stateDir)
}

func (e ManagedProjectEntry) Validate(projectID string) error {
	return validateManagedProjectEntry(projectID, e)
}

func (r ManagedProjectRegistry) CanonicalJSON() ([]byte, error) {
	canonical, err := canonicalizeManagedProjectRegistry(r)
	if err != nil {
		return nil, err
	}
	if err := canonical.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(canonical)
}

func (r ManagedProjectRegistry) Digest() (string, error) {
	data, err := r.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ManagedProjectRegistryDigest(r ManagedProjectRegistry) (string, error) {
	return r.Digest()
}
