package session

import (
	"fmt"
	"os"
	"path/filepath"
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

type Record struct {
	SchemaVersion        int        `json:"schema_version"`
	ID                   string     `json:"session_id"`
	ProjectID            string     `json:"project_id,omitempty"`
	ProjectCode          string     `json:"project_code,omitempty"`
	Role                 string     `json:"role"`
	SessionType          string     `json:"session_type"`
	SessionRef           *string    `json:"session_ref,omitempty"`
	Label                *string    `json:"label,omitempty"`
	Status               string     `json:"status"`
	CreatedAt            time.Time  `json:"created_at"`
	StartedAt            time.Time  `json:"started_at"`
	EndedAt              *time.Time `json:"ended_at,omitempty"`
	UpdatedAt            time.Time  `json:"updated_at"`
	GlobalRulesRevision  string     `json:"global_rules_revision,omitempty"`
	GlobalRulesDigest    string     `json:"global_rules_digest,omitempty"`
	ProjectRulesRevision int        `json:"project_rules_revision,omitempty"`
	ProjectRulesDigest   string     `json:"project_rules_digest,omitempty"`
}

type CreateInput struct {
	ProjectID   string
	ProjectCode string
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
	StateDir         string
	MaxReadBytes     int64
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
	return s.create(input, true)
}

func (s Store) CreateUnbound(role string, label *string) (Record, error) {
	return s.create(CreateInput{
		Role:        role,
		SessionType: SessionTypeChatGPT,
		Label:       label,
	}, false)
}

func (s Store) create(input CreateInput, requireProject bool) (Record, error) {
	if err := validateCreateInput(input, requireProject); err != nil {
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
		id, err := s.nextID(input.Role, input.ProjectCode)
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
			ProjectCode:   input.ProjectCode,
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

func (s Store) Bind(id, projectID string) (Record, error) {
	if strings.TrimSpace(projectID) == "" {
		return Record{}, fmt.Errorf("%w: project_id is required", ErrInvalidSession)
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
	if record.ProjectID != "" && record.ProjectID != projectID {
		return Record{}, fmt.Errorf("%w: session project is immutable", ErrInvalidSession)
	}
	if record.ProjectID == "" {
		record.ProjectID = projectID
		record.ProjectRulesRevision = 0
		record.ProjectRulesDigest = ""
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

func (s Store) AcknowledgeRules(id, globalRevision, globalDigest string, projectRevision int, projectDigest string) (Record, error) {
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
	if record.ProjectID == "" && projectRevision != 0 {
		return Record{}, fmt.Errorf("%w: cannot acknowledge project rules before binding", ErrInvalidSession)
	}
	record.GlobalRulesRevision = globalRevision
	record.GlobalRulesDigest = globalDigest
	record.ProjectRulesRevision = projectRevision
	record.ProjectRulesDigest = projectDigest
	record.UpdatedAt = time.Now().UTC()
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if err := fsutil.WriteJSONAtomic(s.path(id), record, 0o600); err != nil {
		return Record{}, err
	}
	return record, nil
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
