package airelay

import (
	"bytes"
	"fmt"
	"regexp"
	"time"
)

const (
	// MaxTransportMessageBytes is the Airelay wire limit. Prompt content is
	// kept below this so the server-owned provenance marker always fits.
	MaxTransportMessageBytes = 256
	MaxPromptBytes           = 240
)

var sessionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var provenanceRE = regexp.MustCompile(`^(?:S|SP|SD|SA|SW)-(?:[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}|[A-Z]{3}-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{4})$`)

type Result struct {
	ExitCode   int       `json:"exit_code"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}
type TranscriptLine struct {
	Timestamp int64  `json:"timestamp"`
	Text      string `json:"text"`
}
type TranscriptResult struct {
	Lines      []TranscriptLine
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
}
type InterruptResult struct {
	Outcome   string `json:"outcome"`
	Requested bool   `json:"requested"`
	ElapsedMS int    `json:"elapsed_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	Reason    string `json:"reason,omitempty"`
}
type SessionStatus struct {
	State               string   `json:"state"`
	ControllerReachable bool     `json:"controller_reachable"`
	AirelayVersion      string   `json:"airelay_version,omitempty"`
	ProtocolVersion     string   `json:"protocol_version,omitempty"`
	CapacityWarnings    []string `json:"capacity_warnings"`
	ExitCode            int      `json:"exit_code"`
	Error               string   `json:"error,omitempty"`
}
type Client struct {
	Command         string
	Timeout         time.Duration
	MaxMessageBytes int
}

// MessageValidationError is safe to expose through structured MCP errors.
// ActualBytes counts UTF-8 bytes, matching the Airelay transport contract.
type MessageValidationError struct {
	Code        string `json:"code"`
	Reason      string `json:"reason"`
	LimitBytes  int    `json:"limit_bytes,omitempty"`
	ActualBytes int    `json:"actual_bytes,omitempty"`
}

func (e *MessageValidationError) Error() string {
	if e == nil {
		return "invalid Airelay message"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Reason)
}

func (e *MessageValidationError) StructuredActionError() map[string]any {
	return map[string]any{
		"code":  "AIRELAY_MESSAGE_INVALID",
		"phase": "validate",
		"details": map[string]any{
			"reason": e.Reason, "message_code": e.Code,
			"limit_bytes": e.LimitBytes, "actual_bytes": e.ActualBytes,
		},
	}
}

type tailBuffer struct {
	bytes.Buffer
	max      int
	exceeded bool
}
