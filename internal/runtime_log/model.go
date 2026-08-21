package runtime_log

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultMaxBytes    = 1 << 20
	DefaultRetention   = 3
	DefaultLimit       = 100
	MaxLimit           = 200
	MaxCursorBytes     = 4096
	MaxTextBytes       = 512
	MaxIdentifierBytes = 128
)

type contextKey string

const (
	requestIDKey   contextKey = "runtime-log-request-id"
	operationIDKey contextKey = "runtime-log-operation-id"
	actionKey      contextKey = "runtime-log-action"
)

type Event struct {
	Timestamp      time.Time `json:"timestamp"`
	Level          string    `json:"level"`
	Component      string    `json:"component"`
	Event          string    `json:"event"`
	Action         string    `json:"action,omitempty"`
	ErrorCode      string    `json:"error_code,omitempty"`
	Phase          string    `json:"phase,omitempty"`
	RequestID      string    `json:"request_id,omitempty"`
	OperationID    string    `json:"operation_id,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	ProjectID      string    `json:"project_id,omitempty"`
	PID            int       `json:"pid,omitempty"`
	StartTimeTicks uint64    `json:"start_time_ticks,omitempty"`
	Source         string    `json:"source,omitempty"`
	Version        string    `json:"version,omitempty"`
	Signal         string    `json:"signal,omitempty"`
	ExecTimeMS     int64     `json:"exec_time_ms,omitempty"`
	Message        string    `json:"message,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type Filter struct {
	Limit       int
	Cursor      string
	Level       string
	Component   string
	Event       string
	Action      string
	RequestID   string
	SessionID   string
	ProjectID   string
	OperationID string
}

type ReadResult struct {
	Events         []Event `json:"events"`
	MalformedLines int     `json:"malformed_lines"`
	NextCursor     string  `json:"next_cursor"`
	HasMore        bool    `json:"has_more"`
}

func NewRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
	}
	return "req-" + hex.EncodeToString(raw[:])
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, boundedIdentifier(id))
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func WithOperationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, operationIDKey, boundedIdentifier(id))
}

func OperationID(ctx context.Context) string {
	value, _ := ctx.Value(operationIDKey).(string)
	return value
}

func WithAction(ctx context.Context, action string) context.Context {
	return context.WithValue(ctx, actionKey, boundedIdentifier(action))
}

func Action(ctx context.Context) string {
	value, _ := ctx.Value(actionKey).(string)
	return value
}

func (e Event) Validate() error {
	if e.Timestamp.IsZero() || e.Timestamp.Location() != time.UTC {
		return fmt.Errorf("runtime event timestamp must be UTC")
	}
	if !validToken(e.Level) || !validToken(e.Component) || !validToken(e.Event) {
		return fmt.Errorf("runtime event identity is invalid")
	}
	for name, value := range map[string]string{
		"action": e.Action, "error_code": e.ErrorCode, "phase": e.Phase,
		"request_id": e.RequestID, "operation_id": e.OperationID,
		"session_id": e.SessionID, "project_id": e.ProjectID, "source": e.Source,
		"version": e.Version, "signal": e.Signal,
	} {
		if value != "" && !validBoundedString(value, MaxIdentifierBytes) {
			return fmt.Errorf("runtime event %s is invalid", name)
		}
	}
	for name, value := range map[string]string{"message": e.Message, "error": e.Error} {
		if len(value) > MaxTextBytes || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("runtime event %s is invalid", name)
		}
	}
	if e.PID < 0 {
		return fmt.Errorf("runtime event PID is invalid")
	}
	if e.ExecTimeMS < 0 {
		return fmt.Errorf("runtime event execution time is invalid")
	}
	return nil
}

func SanitizeText(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	for _, marker := range []string{"authorization=", "api_key=", "api-key=", "secret=", "token=", "password="} {
		if index := strings.Index(strings.ToLower(value), marker); index >= 0 {
			value = value[:index] + "[redacted]"
		}
	}
	if len(value) > MaxTextBytes {
		value = value[:MaxTextBytes]
	}
	return value
}

func boundedIdentifier(value string) string {
	if len(value) > MaxIdentifierBytes {
		return value[:MaxIdentifierBytes]
	}
	return value
}

func validToken(value string) bool {
	return validBoundedString(value, MaxIdentifierBytes)
}

func validBoundedString(value string, max int) bool {
	if value == "" || len(value) > max || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func normalizeFilter(filter Filter) (Filter, error) {
	if filter.Limit == 0 {
		filter.Limit = DefaultLimit
	}
	if filter.Limit < 1 || filter.Limit > MaxLimit {
		return Filter{}, fmt.Errorf("runtime log limit must be between 1 and %d", MaxLimit)
	}
	if len(filter.Cursor) > MaxCursorBytes {
		return Filter{}, fmt.Errorf("runtime log cursor exceeds %d bytes", MaxCursorBytes)
	}
	for name, value := range map[string]string{
		"level": filter.Level, "component": filter.Component, "event": filter.Event,
		"action": filter.Action, "request_id": filter.RequestID, "session_id": filter.SessionID,
		"project_id": filter.ProjectID, "operation_id": filter.OperationID,
	} {
		if value != "" && !validToken(value) {
			return Filter{}, fmt.Errorf("runtime log %s filter is invalid", name)
		}
	}
	return filter, nil
}
