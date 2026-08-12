package session

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const (
	SchemaVersion           = 1
	RolePlanner             = "planner"
	RoleDelivery            = "delivery"
	RoleAgent               = "agent"
	RoleWatcher             = "watcher"
	SessionIDPrefixLegacy   = "S"
	SessionIDPrefixPlanner  = "SP"
	SessionIDPrefixDelivery = "SD"
	SessionIDPrefixAgent    = "SA"
	SessionIDPrefixWatcher  = "SW"
	SessionTypeChatGPT      = "chatgpt"
	StatusActive            = "active"
	StatusEnded             = "ended"
	maxRecordBytes          = 64 << 10
	maxCreateAttempts       = 16
)

var sessionIDRE = regexp.MustCompile(`^(?:S|SP|SD|SA|SW)-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}$`)

var (
	ErrNotFound       = errors.New("session not found")
	ErrAlreadyEnded   = errors.New("session is already ended")
	ErrInvalidSession = errors.New("invalid session")
)

type Record struct {
	SchemaVersion int        `json:"schema_version"`
	ID            string     `json:"session_id"`
	ProjectID     string     `json:"project_id"`
	Role          string     `json:"role"`
	SessionType   string     `json:"session_type"`
	SessionRef    *string    `json:"session_ref,omitempty"`
	Label         *string    `json:"label,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type CreateInput struct {
	ProjectID   string
	Role        string
	SessionType string
	SessionRef  *string
	Label       *string
}

type UpdateInput struct {
	SessionRef *string
	Label      *string
}

type Store struct {
	StateDir     string
	MaxReadBytes int64
	// IDGenerator is retained for deterministic legacy-fixture tests and
	// migration reads. New default IDs are role-typed; callers that need a
	// custom generator for new records should use TypedIDGenerator.
	IDGenerator      func() (string, error)
	TypedIDGenerator func(string) (string, error)
}

func NewStore(stateDir string) Store {
	return Store{
		StateDir:     stateDir,
		MaxReadBytes: maxRecordBytes,
	}
}

func (s Store) Create(input CreateInput) (Record, error) {
	if err := validateCreateInput(input); err != nil {
		return Record{}, err
	}
	if err := s.ensureRoot(); err != nil {
		return Record{}, err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.StateDir, "locks"), "sessions")
	if err != nil {
		return Record{}, fmt.Errorf("acquire session store lock: %w", err)
	}
	defer lock.Release()
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		id, err := s.nextID(input.Role)
		if err != nil {
			return Record{}, err
		}
		if !sessionIDRE.MatchString(id) {
			return Record{}, fmt.Errorf("%w: generated invalid session ID", ErrInvalidSession)
		}
		path := s.path(id)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return Record{}, fmt.Errorf("inspect session %s: %w", id, err)
		}
		now := time.Now().UTC()
		record := Record{
			SchemaVersion: SchemaVersion,
			ID:            id,
			ProjectID:     input.ProjectID,
			Role:          input.Role,
			SessionType:   input.SessionType,
			SessionRef:    cloneString(input.SessionRef),
			Label:         cloneString(input.Label),
			Status:        StatusActive,
			CreatedAt:     now,
			StartedAt:     now,
			UpdatedAt:     now,
		}
		if err := record.Validate(); err != nil {
			return Record{}, err
		}
		if err := fsutil.WriteJSONAtomic(path, record, 0o600); err != nil {
			return Record{}, fmt.Errorf("write session %s: %w", id, err)
		}
		return record, nil
	}
	return Record{}, fmt.Errorf("session ID allocation exhausted after %d attempts", maxCreateAttempts)
}

func (s Store) Get(id string) (Record, error) {
	if !sessionIDRE.MatchString(id) {
		return Record{}, fmt.Errorf("%w: invalid session ID", ErrInvalidSession)
	}
	path := s.path(id)
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("inspect session %s: %w", id, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Record{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Record{}, fmt.Errorf("session %s is not a regular file", id)
	}
	var record Record
	limit := s.MaxReadBytes
	if limit < 1 || limit > maxRecordBytes {
		limit = maxRecordBytes
	}
	if err := fsutil.ReadJSONBounded(path, limit, &record); err != nil {
		return Record{}, err
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if record.ID != id {
		return Record{}, fmt.Errorf("session file identity mismatch")
	}
	return record, nil
}

func (s Store) Update(id string, input UpdateInput) (Record, error) {
	if err := validateUpdateInput(input); err != nil {
		return Record{}, err
	}
	lock, err := s.acquireMutationLock()
	if err != nil {
		return Record{}, err
	}
	defer lock.Release()
	record, err := s.Get(id)
	if err != nil {
		return Record{}, err
	}
	if record.Status != StatusActive {
		return Record{}, ErrAlreadyEnded
	}
	if input.SessionRef != nil {
		record.SessionRef = cloneString(input.SessionRef)
	}
	if input.Label != nil {
		record.Label = cloneString(input.Label)
	}
	record.UpdatedAt = time.Now().UTC()
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if err := fsutil.WriteJSONAtomic(s.path(id), record, 0o600); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s Store) End(id string) (Record, error) {
	lock, err := s.acquireMutationLock()
	if err != nil {
		return Record{}, err
	}
	defer lock.Release()
	record, err := s.Get(id)
	if err != nil {
		return Record{}, err
	}
	if record.Status == StatusEnded {
		return record, nil
	}
	now := time.Now().UTC()
	record.Status = StatusEnded
	record.EndedAt = &now
	record.UpdatedAt = now
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if err := fsutil.WriteJSONAtomic(s.path(id), record, 0o600); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.sessionsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !sessionIDRE.MatchString(strings.TrimSuffix(entry.Name(), ".json")) || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
	}
	sort.Strings(ids)
	result := make([]Record, 0, len(ids))
	for _, id := range ids {
		record, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func (r Record) Validate() error {
	if r.SchemaVersion != SchemaVersion || !sessionIDRE.MatchString(r.ID) || !sessionIDMatchesRole(r.ID, r.Role) || strings.TrimSpace(r.ProjectID) == "" || !validRole(r.Role) || !validSessionType(r.SessionType) {
		return fmt.Errorf("%w: invalid session record", ErrInvalidSession)
	}
	if r.Status != StatusActive && r.Status != StatusEnded {
		return fmt.Errorf("%w: invalid session status", ErrInvalidSession)
	}
	if r.CreatedAt.IsZero() || r.StartedAt.IsZero() || r.UpdatedAt.IsZero() || r.StartedAt.Before(r.CreatedAt) || r.UpdatedAt.Before(r.CreatedAt) {
		return fmt.Errorf("%w: invalid session timestamps", ErrInvalidSession)
	}
	if r.Status == StatusActive && r.EndedAt != nil {
		return fmt.Errorf("%w: active session has ended_at", ErrInvalidSession)
	}
	if r.Status == StatusEnded && (r.EndedAt == nil || r.EndedAt.Before(r.StartedAt)) {
		return fmt.Errorf("%w: ended session has invalid ended_at", ErrInvalidSession)
	}
	if err := validateOptionalText(r.SessionRef, "session_ref"); err != nil {
		return err
	}
	return validateOptionalText(r.Label, "label")
}

func validRole(role string) bool {
	return role == RolePlanner || role == RoleDelivery || role == RoleAgent || role == RoleWatcher
}

func sessionIDMatchesRole(id, role string) bool {
	// S-* is the pre-typed identifier format. It remains readable for
	// migration, but carries no authority and therefore has no role mapping.
	if strings.HasPrefix(id, SessionIDPrefixLegacy+"-") {
		return true
	}
	prefix, _, ok := strings.Cut(id, "-")
	if !ok {
		return false
	}
	want := map[string]string{
		RolePlanner:  SessionIDPrefixPlanner,
		RoleDelivery: SessionIDPrefixDelivery,
		RoleAgent:    SessionIDPrefixAgent,
		RoleWatcher:  SessionIDPrefixWatcher,
	}[role]
	return want != "" && prefix == want
}

func validSessionType(value string) bool { return value == SessionTypeChatGPT }

func validateCreateInput(input CreateInput) error {
	if strings.TrimSpace(input.ProjectID) == "" || !validRole(input.Role) || !validSessionType(input.SessionType) {
		return fmt.Errorf("%w: invalid session creation request", ErrInvalidSession)
	}
	if err := validateOptionalText(input.SessionRef, "session_ref"); err != nil {
		return err
	}
	return validateOptionalText(input.Label, "label")
}

func validateUpdateInput(input UpdateInput) error {
	if input.SessionRef == nil && input.Label == nil {
		return fmt.Errorf("%w: session update has no mutable fields", ErrInvalidSession)
	}
	if err := validateOptionalText(input.SessionRef, "session_ref"); err != nil {
		return err
	}
	return validateOptionalText(input.Label, "label")
}

func validateOptionalText(value *string, name string) error {
	if value != nil && len([]byte(*value)) > 256 {
		return fmt.Errorf("%s exceeds 256 bytes", name)
	}
	return nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s Store) ensureRoot() error { return fsutil.EnsureDir(s.sessionsDir(), 0o700) }

func (s Store) acquireMutationLock() (*lockfile.Lock, error) {
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.StateDir, "locks"), "sessions")
	if err != nil {
		return nil, fmt.Errorf("acquire session store lock: %w", err)
	}
	return lock, nil
}

func (s Store) sessionsDir() string   { return filepath.Join(s.StateDir, "sessions") }
func (s Store) path(id string) string { return filepath.Join(s.sessionsDir(), id+".json") }

func (s Store) nextID(role string) (string, error) {
	if s.IDGenerator != nil {
		return s.IDGenerator()
	}
	if s.TypedIDGenerator != nil {
		return s.TypedIDGenerator(role)
	}
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var value uint64
	for _, b := range raw {
		value = value<<8 | uint64(b)
	}
	var encoded [8]byte
	for i := len(encoded) - 1; i >= 0; i-- {
		encoded[i] = alphabet[value&31]
		value >>= 5
	}
	prefix := map[string]string{
		RolePlanner:  SessionIDPrefixPlanner,
		RoleDelivery: SessionIDPrefixDelivery,
		RoleAgent:    SessionIDPrefixAgent,
		RoleWatcher:  SessionIDPrefixWatcher,
	}[role]
	if prefix == "" {
		return "", fmt.Errorf("%w: unsupported session role", ErrInvalidSession)
	}
	return prefix + "-" + string(encoded[:]), nil
}
