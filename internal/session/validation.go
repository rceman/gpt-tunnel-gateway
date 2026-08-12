package session

import (
	"crypto/rand"
	"fmt"
	"strings"
)

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
