package session

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const workflowSessionIDPattern = `[A-Z]{3}_[A-Z]{3}_[PLAW]_[a-z0-9]{5}`
const adminSessionIDPattern = `[A-Z]{3}_ADM_[a-z0-9]{32}`

var sessionIDRE = regexp.MustCompile("^" + workflowSessionIDPattern + "$")
var adminSessionIDRE = regexp.MustCompile("^" + adminSessionIDPattern + "$")

func CanonicalSessionIDPattern() string {
	return "^(?:" + workflowSessionIDPattern + "|" + adminSessionIDPattern + ")$"
}

var sessionGatewayKeyRE = regexp.MustCompile(`^[A-Z]{3}$`)
var sessionProjectCodeRE = regexp.MustCompile(`^[A-Z]{3}$`)

var (
	ErrNotFound       = errors.New("session not found")
	ErrAlreadyEnded   = errors.New("session is already ended")
	ErrInvalidSession = errors.New("invalid session")
)

// IsCanonicalSessionID reports whether value uses the durable workflow Session identity format.
func IsCanonicalSessionID(value string) bool {
	return sessionIDRE.MatchString(value) || adminSessionIDRE.MatchString(value)
}

func IsAdminSessionID(value string) bool {
	return adminSessionIDRE.MatchString(value)
}

func (r Record) Validate() error {
	if err := validateRecordShape(r); err != nil {
		return err
	}
	if r.Role == RoleAdmin {
		if !IsAdminSessionID(r.ID) || r.ProjectID != "" || r.ProjectCode != "" || r.SessionRef != nil {
			return fmt.Errorf("%w: invalid admin session record", ErrInvalidSession)
		}
		return nil
	}
	if !sessionIDMatchesRole(r.ID, r.Role) || !validRole(r.Role) {
		return fmt.Errorf("%w: invalid session record", ErrInvalidSession)
	}
	return nil
}

func validateRecordShape(r Record) error {
	if r.SchemaVersion != SchemaVersion || !IsCanonicalSessionID(r.ID) || !validSessionType(r.SessionType) {
		return fmt.Errorf("%w: invalid session record", ErrInvalidSession)
	}
	if r.Role == RoleAdmin {
		if r.SessionType != SessionTypeAdmin || r.ProjectID != "" || r.ProjectCode != "" {
			return fmt.Errorf("%w: invalid admin session record", ErrInvalidSession)
		}
		if err := validateSessionTimestamps(r); err != nil {
			return err
		}
		return validateOptionalText(r.Label, "label")
	}
	if r.ProjectCode == "" {
		return fmt.Errorf("%w: bound session project code is required", ErrInvalidSession)
	}
	if err := validateProjectCode(r.ProjectCode); err != nil {
		return err
	}
	if sessionIDProjectCode(r.ID) != r.ProjectCode {
		return fmt.Errorf("%w: session project code does not match session ID", ErrInvalidSession)
	}
	if strings.TrimSpace(r.ProjectID) == "" && r.ProjectRulesDigest != "" {
		return fmt.Errorf("%w: unbound session has project rules acknowledgement", ErrInvalidSession)
	}
	if strings.TrimSpace(r.ProjectID) == "" {
		return fmt.Errorf("%w: bound session project is required", ErrInvalidSession)
	}
	if err := validateSessionTimestamps(r); err != nil {
		return err
	}
	if WorkflowRoleRequiresRef(r.Role) && (r.SessionRef == nil || strings.TrimSpace(*r.SessionRef) == "") {
		return fmt.Errorf("%w: managed role requires a server-owned binding", ErrInvalidSession)
	}
	if err := validateOptionalText(r.SessionRef, "session_ref"); err != nil {
		return err
	}
	return validateOptionalText(r.Label, "label")
}

func validateSessionTimestamps(r Record) error {
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
	return nil
}

func validateGatewayKey(value string) error {
	if !sessionGatewayKeyRE.MatchString(value) {
		return fmt.Errorf("%w: gateway key must be three uppercase letters", ErrInvalidSession)
	}
	return nil
}

func validateProjectCode(value string) error {
	if !sessionProjectCodeRE.MatchString(value) {
		return fmt.Errorf("%w: project code must be three uppercase letters", ErrInvalidSession)
	}
	return nil
}

func sessionIDProjectCode(id string) string {
	parts := strings.Split(id, "_")
	if len(parts) == 4 && sessionProjectCodeRE.MatchString(parts[1]) {
		return parts[1]
	}
	return ""
}

func validRole(role string) bool {
	return IsWorkflowRole(role) || role == RoleAdmin
}

func sessionIDMatchesRole(id, role string) bool {
	parts := strings.Split(id, "_")
	if len(parts) != 4 {
		return false
	}
	workflowRole, ok := WorkflowRoleByCode(parts[2])
	return ok && workflowRole.Key == role
}

func validSessionType(value string) bool {
	return value == SessionTypeChatGPT || value == SessionTypeAdmin
}

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
	if WorkflowRoleRequiresRef(input.Role) && (input.SessionRef == nil || strings.TrimSpace(*input.SessionRef) == "") {
		return fmt.Errorf("%w: managed role requires a server-owned binding", ErrInvalidSession)
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
