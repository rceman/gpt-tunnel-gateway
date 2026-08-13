package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	RuleIDPattern       = `^[A-Z]{3}-RUL(` + OperatorJournalNumberPattern + `)$`
	MessageIDPattern    = `^[A-Z]{3}-MSG(` + OperatorJournalNumberPattern + `)$`
	JournalIDPattern    = `^[A-Z]{3}-JRN(` + OperatorJournalNumberPattern + `)$`
	MaxRuleNameBytes    = 256
	MaxRuleTextBytes    = 8192
	MaxMessageTextBytes = 16384
)

type Rule struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Message struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	SessionID     *string   `json:"session_id"`
	Role          string    `json:"role"`
	Content       string    `json:"content"`
	CreatedAt     time.Time `json:"created_at"`
}

// JournalEvent is the canonical journal record. OperatorJournalEvent remains
// the wire-compatible type for historical OPR records and existing callers.
type JournalEvent = OperatorJournalEvent

func FormatRuleID(projectCode string, number uint64) (string, error) {
	return formatEntityID(projectCode, "RUL", number)
}

func FormatMessageID(projectCode string, number uint64) (string, error) {
	return formatEntityID(projectCode, "MSG", number)
}

func FormatJournalID(projectCode string, number uint64) (string, error) {
	return formatEntityID(projectCode, "JRN", number)
}

func ParseRuleID(value string) (string, uint64, error)    { return parseEntityID(value, "RUL") }
func ParseMessageID(value string) (string, uint64, error) { return parseEntityID(value, "MSG") }
func ParseJournalID(value string) (string, uint64, error) { return parseEntityID(value, "JRN") }

func ValidateRuleID(value string) error    { _, _, err := ParseRuleID(value); return err }
func ValidateMessageID(value string) error { _, _, err := ParseMessageID(value); return err }
func ValidateJournalID(value string) error { _, _, err := ParseJournalID(value); return err }

func ValidateRule(v Rule) error {
	if v.SchemaVersion != SchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || ValidateRuleID(v.ID) != nil {
		return fmt.Errorf("invalid rule identity")
	}
	if len(v.Name) == 0 || len(v.Name) > MaxRuleNameBytes || strings.TrimSpace(v.Name) != v.Name || len(v.Description) > MaxRuleTextBytes || strings.ContainsRune(v.Description, 0) {
		return fmt.Errorf("invalid rule content")
	}
	if v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.CreatedAt.After(v.UpdatedAt) {
		return fmt.Errorf("invalid rule timestamps")
	}
	return nil
}

func ValidateMessage(v Message) error {
	if v.SchemaVersion != SchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || ValidateMessageID(v.ID) != nil {
		return fmt.Errorf("invalid message identity")
	}
	if v.Role == "" || len(v.Role) > MaxRuleNameBytes || strings.TrimSpace(v.Role) != v.Role || len(v.Content) == 0 || len(v.Content) > MaxMessageTextBytes || strings.ContainsRune(v.Content, 0) {
		return fmt.Errorf("invalid message content")
	}
	if v.SessionID != nil && (len(*v.SessionID) == 0 || len(*v.SessionID) > MaxOperatorSessionIDBytes) {
		return fmt.Errorf("invalid message session_id")
	}
	if v.CreatedAt.IsZero() {
		return fmt.Errorf("invalid message timestamp")
	}
	return nil
}

func formatEntityID(projectCode, family string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s%d", projectCode, family, number), nil
}

func parseEntityID(value, family string) (string, uint64, error) {
	if len(value) < 8 || value[3:4] != "-" || !strings.HasPrefix(value[4:], family) {
		return "", 0, fmt.Errorf("invalid canonical %s identifier", family)
	}
	code := value[:3]
	if err := ValidateProjectCode(code); err != nil {
		return "", 0, err
	}
	number, err := parseCompactIDNumber(value[4+len(family):])
	if err != nil {
		return "", 0, fmt.Errorf("invalid canonical %s identifier", family)
	}
	return code, number, nil
}
