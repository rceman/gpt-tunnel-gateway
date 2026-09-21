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
	MaxMessageTextBytes = 16384
)

type Message struct {
	SchemaVersion int        `json:"schema_version"`
	ID            string     `json:"message"`
	ProjectID     string     `json:"project_id"`
	ProjectCode   string     `json:"-"`
	SessionID     *string    `json:"session_id,omitempty"`
	Role          string     `json:"role,omitempty"`
	Content       string     `json:"content,omitempty"`
	FromRole      string     `json:"from_role"`
	FromSession   string     `json:"from_session"`
	ToRole        string     `json:"to_role"`
	Body          string     `json:"body"`
	Title         string     `json:"title,omitempty"`
	InReplyTo     string     `json:"in_reply_to,omitempty"`
	State         string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ReadAt        *time.Time `json:"read_at,omitempty"`
	ReadSession   string     `json:"read_session,omitempty"`
	CancelledAt   *time.Time `json:"-"`
	CancelledBy   string     `json:"cancelled_by,omitempty"`
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
