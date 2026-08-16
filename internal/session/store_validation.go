package session

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var sessionIDRE = regexp.MustCompile(`^(?:S|SP|SD|SA|SW)-(?:[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}|[A-Z]{3}-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{4})$`)
var sessionProjectCodeRE = regexp.MustCompile(`^[A-Z]{3}$`)

var (
	ErrNotFound       = errors.New("session not found")
	ErrAlreadyEnded   = errors.New("session is already ended")
	ErrInvalidSession = errors.New("invalid session")
)

func (r Record) Validate() error {
	if r.SchemaVersion != SchemaVersion || !sessionIDRE.MatchString(r.ID) || !sessionIDMatchesRole(r.ID, r.Role) || !validRole(r.Role) || !validSessionType(r.SessionType) {
		return fmt.Errorf("%w: invalid session record", ErrInvalidSession)
	}
	if r.ProjectCode != "" {
		if err := validateProjectCode(r.ProjectCode); err != nil {
			return err
		}
		if sessionIDProjectCode(r.ID) != r.ProjectCode {
			return fmt.Errorf("%w: session project code does not match session ID", ErrInvalidSession)
		}
	}
	if strings.TrimSpace(r.ProjectID) == "" && r.ProjectRulesRevision != 0 {
		return fmt.Errorf("%w: unbound session has project rules acknowledgement", ErrInvalidSession)
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

func validateProjectCode(value string) error {
	if !sessionProjectCodeRE.MatchString(value) {
		return fmt.Errorf("%w: project code must be three uppercase letters", ErrInvalidSession)
	}
	return nil
}

func sessionIDProjectCode(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) == 3 && sessionProjectCodeRE.MatchString(parts[1]) {
		return parts[1]
	}
	return ""
}

func validRole(role string) bool {
	return role == RolePlanner || role == RoleDelivery || role == RoleAgent || role == RoleWatcher
}

func sessionIDMatchesRole(id, role string) bool {
	if strings.HasPrefix(id, SessionIDPrefixLegacy+"-") {
		return true
	}
	prefix, _, ok := strings.Cut(id, "-")
	if !ok {
		return false
	}
	want := map[string]string{RolePlanner: SessionIDPrefixPlanner, RoleDelivery: SessionIDPrefixDelivery, RoleAgent: SessionIDPrefixAgent, RoleWatcher: SessionIDPrefixWatcher}[role]
	return want != "" && prefix == want
}

func validSessionType(value string) bool { return value == SessionTypeChatGPT }

func validateCreateInput(input CreateInput, requireProject bool) error {
	if (requireProject && strings.TrimSpace(input.ProjectID) == "") || !validRole(input.Role) || !validSessionType(input.SessionType) {
		return fmt.Errorf("%w: invalid session creation request", ErrInvalidSession)
	}
	if input.ProjectCode != "" {
		if err := validateProjectCode(input.ProjectCode); err != nil {
			return err
		}
	} else if requireProject {
		return fmt.Errorf("%w: project code is required for bound sessions", ErrInvalidSession)
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
