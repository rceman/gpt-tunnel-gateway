package model

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MessageSchemaVersion  = SchemaVersion
	MessageStateUnread    = "unread"
	MessageStateRead      = "read"
	MessageStateCancelled = "cancelled"
	MessageBodyMaxBytes   = 16384
	MessageTitleMaxChars  = 128
	MessagePageMax        = 100
)

func MessageRoles() []string {
	return []string{"planner", "lead", "advisor", "worker"}
}

func IsMessageRole(role string) bool {
	switch role {
	case "planner", "lead", "advisor", "worker":
		return true
	default:
		return false
	}
}

func MessageStates() []string {
	return []string{MessageStateUnread, MessageStateRead, MessageStateCancelled}
}

func ValidateMessage(v Message) error {
	if v.FromRole != "" || v.ToRole != "" || v.Body != "" || v.FromSession != "" {
		return ValidatePLAWMessage(v)
	}
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

func ValidatePLAWMessage(v Message) error {
	if v.SchemaVersion != MessageSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || ValidateProjectCode(v.ProjectCode) != nil {
		return fmt.Errorf("invalid message identity")
	}
	if err := ValidateMessageID(v.ID); err != nil || !strings.HasPrefix(v.ID, v.ProjectCode+"-MSG") {
		return fmt.Errorf("invalid message identifier")
	}
	if !IsMessageRole(v.FromRole) || !IsMessageRole(v.ToRole) || v.FromSession == "" || v.CreatedAt.IsZero() {
		return fmt.Errorf("invalid message route")
	}
	if !utf8.ValidString(v.Body) || len([]byte(v.Body)) == 0 || len([]byte(v.Body)) > MessageBodyMaxBytes || strings.ContainsRune(v.Body, '\x00') {
		return fmt.Errorf("message body is empty or exceeds %d bytes", MessageBodyMaxBytes)
	}
	if !utf8.ValidString(v.Title) || utf8.RuneCountInString(v.Title) > MessageTitleMaxChars || strings.ContainsRune(v.Title, '\x00') {
		return fmt.Errorf("message title exceeds %d characters", MessageTitleMaxChars)
	}
	if v.State != MessageStateUnread && v.State != MessageStateRead && v.State != MessageStateCancelled {
		return fmt.Errorf("invalid message state")
	}
	if v.State == MessageStateRead && (v.ReadAt == nil || v.ReadSession == "") {
		return fmt.Errorf("read message requires read metadata")
	}
	if v.State == MessageStateUnread && (v.ReadAt != nil || v.ReadSession != "") {
		return fmt.Errorf("unread message has read metadata")
	}
	if v.State == MessageStateCancelled && (v.CancelledAt == nil || v.CancelledBy == "") {
		return fmt.Errorf("cancelled message requires cancelled metadata")
	}
	return nil
}
