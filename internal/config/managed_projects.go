package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
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
	Root              string `json:"root"`
	RepositoryURL     string `json:"repository_url"`
	Remote            string `json:"remote"`
	DefaultBranch     string `json:"default_branch"`
	AirelaySessionKey string `json:"airelay_session_key"`
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
	return ManagedProjectRegistry{SchemaVersion: ManagedProjectRegistrySchemaVersion, Projects: map[string]ManagedProjectEntry{}}
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

func LoadManagedProjectRegistry(path string) (ManagedProjectRegistry, error) {
	return loadManagedProjectRegistry(path)
}

func LoadManagedProjects(stateDir string) (ManagedProjectRegistry, error) {
	return loadManagedProjectRegistry(ManagedProjectRegistryPath(stateDir))
}

func loadManagedProjectRegistry(path string) (ManagedProjectRegistry, error) {
	if path == "" {
		return ManagedProjectRegistry{}, fmt.Errorf("managed project registry path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EmptyManagedProjectRegistry(), nil
		}
		return ManagedProjectRegistry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ManagedProjectRegistry{}, fmt.Errorf("managed project registry must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return ManagedProjectRegistry{}, fmt.Errorf("managed project registry is not a regular file")
	}
	data, err := fsutil.ReadFileBounded(path, ManagedProjectRegistryMaxBytes)
	if err != nil {
		return ManagedProjectRegistry{}, err
	}
	var registry ManagedProjectRegistry
	if err := decodeManagedJSON(data, &registry); err != nil {
		return ManagedProjectRegistry{}, fmt.Errorf("decode managed project registry: %w", err)
	}
	registry, err = canonicalizeManagedProjectRegistry(registry)
	if err != nil {
		return ManagedProjectRegistry{}, err
	}
	if err := registry.ValidateForStateDir(filepath.Dir(path)); err != nil {
		return ManagedProjectRegistry{}, err
	}
	return registry, nil
}

func WriteManagedProjectRegistry(stateDir, expectedDigest string, next ManagedProjectRegistry) (ManagedProjectRegistryWriteReceipt, error) {
	if err := validateStateDir(stateDir); err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	stateDir = filepath.Clean(stateDir)
	path := ManagedProjectRegistryPath(stateDir)
	lock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "managed-projects")
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	defer lock.Release()

	current, err := loadManagedProjectRegistry(path)
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	beforeDigest, err := current.Digest()
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if expectedDigest != beforeDigest {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("MANAGED_PROJECTS_DIGEST_CONFLICT expected=%s actual=%s", expectedDigest, beforeDigest)
	}
	if current.Revision >= MaxManagedProjectRegistryRevision {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("managed project registry revision cannot advance beyond safe integer maximum")
	}
	expectedRevision := current.Revision + 1
	if next.Revision != expectedRevision {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("managed project registry revision must be %d", expectedRevision)
	}
	next, err = canonicalizeManagedProjectRegistry(next)
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if err := next.ValidateForStateDir(stateDir); err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	afterDigest, err := next.Digest()
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if err := fsutil.WriteJSONAtomic(path, next, 0o600); err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	verified, err := loadManagedProjectRegistry(path)
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	verifiedDigest, err := verified.Digest()
	if err != nil {
		return ManagedProjectRegistryWriteReceipt{}, err
	}
	if verifiedDigest != afterDigest || verified.Revision != next.Revision {
		return ManagedProjectRegistryWriteReceipt{}, fmt.Errorf("managed project registry verification failed")
	}
	return ManagedProjectRegistryWriteReceipt{Path: path, BeforeDigest: beforeDigest, AfterDigest: afterDigest, BeforeRevision: current.Revision, AfterRevision: next.Revision}, nil
}

func UpdateManagedProjectRegistry(stateDir, expectedDigest string, next ManagedProjectRegistry) (ManagedProjectRegistryWriteReceipt, error) {
	return WriteManagedProjectRegistry(stateDir, expectedDigest, next)
}

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
		result[id] = ProjectConfig{Root: entry.Root, Mirror: mirror, Remote: entry.Remote, DefaultBranch: entry.DefaultBranch, AirelaySessionKey: entry.AirelaySessionKey}
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

func decodeManagedJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func decodeManagedSafeInteger(data []byte, name string) (uint64, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, fmt.Errorf("%s: trailing JSON content", name)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be a JSON number", name)
	}
	text := number.String()
	if strings.HasPrefix(text, "-") {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}

	mantissa := text
	exponent := 0
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		parsed, err := parseManagedExponent(mantissa[index+1:], len(data)+32)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		exponent = parsed
		mantissa = mantissa[:index]
	}
	parts := strings.Split(mantissa, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return 0, fmt.Errorf("%s must be a valid JSON number", name)
	}
	fracDigits := ""
	if len(parts) == 2 {
		fracDigits = parts[1]
	}
	digits := strings.TrimLeft(parts[0]+fracDigits, "0")
	if digits == "" {
		return 0, nil
	}
	scale := exponent - len(fracDigits)
	if scale >= 0 {
		if len(digits)+scale > 16 {
			return 0, fmt.Errorf("%s exceeds safe integer maximum", name)
		}
		digits += strings.Repeat("0", scale)
	} else {
		cut := -scale
		if cut >= len(digits) {
			return 0, fmt.Errorf("%s must be integral", name)
		}
		fraction := digits[len(digits)-cut:]
		if strings.Trim(fraction, "0") != "" {
			return 0, fmt.Errorf("%s must be integral", name)
		}
		digits = digits[:len(digits)-cut]
	}
	if len(digits) > 16 {
		return 0, fmt.Errorf("%s exceeds safe integer maximum", name)
	}
	parsed, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || parsed > MaxManagedProjectRegistryRevision {
		return 0, fmt.Errorf("%s exceeds safe integer maximum", name)
	}
	return parsed, nil
}

func parseManagedExponent(text string, limit int) (int, error) {
	if text == "" {
		return 0, fmt.Errorf("exponent is empty")
	}
	sign := 1
	if text[0] == '+' || text[0] == '-' {
		if text[0] == '-' {
			sign = -1
		}
		text = text[1:]
	}
	if text == "" {
		return 0, fmt.Errorf("exponent is empty")
	}
	value := 0
	for _, character := range text {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("exponent is invalid")
		}
		digit := int(character - '0')
		if value > (limit-digit)/10 {
			return 0, fmt.Errorf("exponent exceeds bounded range")
		}
		value = value*10 + digit
	}
	return sign * value, nil
}

func decodeManagedObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("object key must be a string")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate JSON field %q", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("unterminated JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON content")
	}
	return fields, nil
}

func (r *ManagedProjectRegistry) UnmarshalJSON(data []byte) error {
	fields, err := decodeManagedObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "schema_version", "revision", "projects":
		default:
			return fmt.Errorf("unknown managed project registry field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "revision", "projects"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("managed project registry field %q is required", key)
		}
	}
	var schemaVersion int
	if err := json.Unmarshal(fields["schema_version"], &schemaVersion); err != nil {
		return fmt.Errorf("schema_version: %w", err)
	}
	revision, err := decodeManagedSafeInteger(fields["revision"], "revision")
	if err != nil {
		return fmt.Errorf("revision: %w", err)
	}
	projectFields, err := decodeManagedObject(fields["projects"])
	if err != nil {
		return fmt.Errorf("projects: %w", err)
	}
	projects := make(map[string]ManagedProjectEntry, len(projectFields))
	for id, raw := range projectFields {
		var entry ManagedProjectEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("projects.%s: %w", id, err)
		}
		projects[id] = entry
	}
	*r = ManagedProjectRegistry{SchemaVersion: schemaVersion, Revision: revision, Projects: projects}
	return nil
}

func (e *ManagedProjectEntry) UnmarshalJSON(data []byte) error {
	fields, err := decodeManagedObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "root", "repository_url", "remote", "default_branch", "airelay_session_key":
		default:
			return fmt.Errorf("unknown managed project field %q", key)
		}
	}
	for _, key := range []string{"root", "repository_url", "remote", "default_branch", "airelay_session_key"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("managed project field %q is required", key)
		}
	}
	var entry ManagedProjectEntry
	for key, target := range map[string]*string{"root": &entry.Root, "repository_url": &entry.RepositoryURL, "remote": &entry.Remote, "default_branch": &entry.DefaultBranch, "airelay_session_key": &entry.AirelaySessionKey} {
		if err := json.Unmarshal(fields[key], target); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	*e = entry
	return nil
}
