package airelay

import (
	"bytes"
	"regexp"
	"time"
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
type tailBuffer struct {
	bytes.Buffer
	max      int
	exceeded bool
}
