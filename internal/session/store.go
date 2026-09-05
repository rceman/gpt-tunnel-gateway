package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const (
	SchemaVersion          = 1
	RolePlanner            = "planner"
	RoleAgent              = "agent"
	SessionIDPrefixLegacy  = "S"
	SessionIDPrefixPlanner = "SP"
	SessionIDPrefixAgent   = "SA"
	SessionTypeChatGPT     = "chatgpt"
	StatusActive           = "active"
	StatusEnded            = "ended"
	maxRecordBytes         = 64 << 10
	maxCreateAttempts      = 16
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
	ProjectID, ProjectCode, Role, SessionType string
	SessionRef, Label                         *string
}
type UpdateInput struct{ SessionRef, Label *string }

// Store is the production Session repository. Local SQLite is its only authority.
type Store struct {
	Durability       *sqlitestore.Databases
	IDGenerator      func() (string, error)
	TypedIDGenerator func(string) (string, error)
}

func NewStoreWithDurability(durability *sqlitestore.Databases) Store {
	return Store{Durability: durability}
}
func (s Store) requireLocal() error {
	if s.Durability == nil || s.Durability.Local == nil {
		return fmt.Errorf("local session store is unavailable")
	}
	return nil
}
func (s Store) Create(input CreateInput) (Record, error) { return s.create(input, true) }
func (s Store) CreateUnbound(role string, label *string) (Record, error) {
	return s.create(CreateInput{Role: role, SessionType: SessionTypeChatGPT, Label: label}, false)
}
func (s Store) create(input CreateInput, requireProject bool) (Record, error) {
	if err := validateCreateInput(input, requireProject); err != nil {
		return Record{}, err
	}
	if err := s.requireLocal(); err != nil {
		return Record{}, err
	}
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		id, err := s.nextID(input.Role, input.ProjectCode)
		if err != nil {
			return Record{}, err
		}
		now := time.Now().UTC()
		record := Record{SchemaVersion: SchemaVersion, ID: id, ProjectID: input.ProjectID, ProjectCode: input.ProjectCode, Role: input.Role, SessionType: input.SessionType, SessionRef: cloneString(input.SessionRef), Label: cloneString(input.Label), Status: StatusActive, CreatedAt: now, StartedAt: now, UpdatedAt: now}
		if err := record.Validate(); err != nil {
			return Record{}, err
		}
		payload, err := json.Marshal(record)
		if err != nil {
			return Record{}, err
		}
		err = s.Durability.CreateLocalSession(context.Background(), sqlitestore.LocalSession{ID: id, Payload: payload, UpdatedAt: now.Format(time.RFC3339Nano), Status: record.Status})
		if errors.Is(err, sqlitestore.ErrLocalSessionExists) {
			continue
		}
		if err != nil {
			return Record{}, err
		}
		return record, nil
	}
	return Record{}, fmt.Errorf("session ID allocation exhausted after %d attempts", maxCreateAttempts)
}
func (s Store) Bind(id, projectID string, sessionRef *string) (Record, error) {
	if projectID == "" {
		return Record{}, fmt.Errorf("%w: project_id is required", ErrInvalidSession)
	}
	if err := validateOptionalText(sessionRef, "session_ref"); err != nil {
		return Record{}, err
	}
	if err := s.requireLocal(); err != nil {
		return Record{}, err
	}
	record, err := s.Get(id)
	if err != nil {
		return Record{}, err
	}
	old := record
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
	if sessionRef != nil {
		record.SessionRef = cloneString(sessionRef)
	}
	record.UpdatedAt = time.Now().UTC()
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	return record, s.updateLocal(old, record)
}
func (s Store) AcknowledgeRules(id, globalRevision, globalDigest string, projectRevision int, projectDigest string) (Record, error) {
	if err := s.requireLocal(); err != nil {
		return Record{}, err
	}
	record, err := s.Get(id)
	if err != nil {
		return Record{}, err
	}
	old := record
	if record.Status != StatusActive {
		return Record{}, ErrAlreadyEnded
	}
	if record.ProjectID == "" && projectRevision != 0 {
		return Record{}, fmt.Errorf("%w: cannot acknowledge project rules before binding", ErrInvalidSession)
	}
	record.GlobalRulesRevision, record.GlobalRulesDigest = globalRevision, globalDigest
	record.ProjectRulesRevision, record.ProjectRulesDigest = projectRevision, projectDigest
	record.UpdatedAt = time.Now().UTC()
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	return record, s.updateLocal(old, record)
}
func (s Store) Get(id string) (Record, error) {
	if !sessionIDRE.MatchString(id) {
		return Record{}, fmt.Errorf("%w: invalid session ID", ErrInvalidSession)
	}
	if err := s.requireLocal(); err != nil {
		return Record{}, err
	}
	row, err := s.Durability.ReadLocalSession(context.Background(), id)
	if errors.Is(err, sqlitestore.ErrLocalSessionNotFound) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	return decodeLocal(row)
}
func (s Store) Update(id string, input UpdateInput) (Record, error) {
	if err := validateUpdateInput(input); err != nil {
		return Record{}, err
	}
	if err := s.requireLocal(); err != nil {
		return Record{}, err
	}
	record, err := s.Get(id)
	if err != nil {
		return Record{}, err
	}
	old := record
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
	return record, s.updateLocal(old, record)
}
func (s Store) End(id string) (Record, error) {
	if err := s.requireLocal(); err != nil {
		return Record{}, err
	}
	record, err := s.Get(id)
	if err != nil {
		return Record{}, err
	}
	if record.Status == StatusEnded {
		return record, nil
	}
	old := record
	now := time.Now().UTC()
	record.Status, record.EndedAt, record.UpdatedAt = StatusEnded, &now, now
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if err := s.updateLocal(old, record); err != nil {
		if errors.Is(err, sqlitestore.ErrLocalSessionChanged) {
			current, readErr := s.Get(id)
			if readErr == nil && current.Status == StatusEnded {
				return current, nil
			}
		}
		return Record{}, err
	}
	return record, nil
}
func (s Store) List() ([]Record, error) {
	if err := s.requireLocal(); err != nil {
		return nil, err
	}
	rows, err := s.Durability.ListLocalSessions(context.Background())
	if err != nil {
		return nil, err
	}
	result := make([]Record, 0, len(rows))
	for _, row := range rows {
		record, err := decodeLocal(row)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}
func (s Store) updateLocal(old, record Record) error {
	oldPayload, err := json.Marshal(old)
	if err != nil {
		return err
	}
	newPayload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.Durability.UpdateLocalSession(context.Background(), record.ID, oldPayload, newPayload, record.UpdatedAt.UTC().Format(time.RFC3339Nano), record.Status)
}
func decodeLocal(row sqlitestore.LocalSession) (Record, error) {
	var record Record
	if err := json.Unmarshal(row.Payload, &record); err != nil {
		return Record{}, err
	}
	if err := record.Validate(); err != nil || record.ID != row.ID || record.Status != row.Status {
		return Record{}, fmt.Errorf("invalid local session record")
	}
	return record, nil
}
