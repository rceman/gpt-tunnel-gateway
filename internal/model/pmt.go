package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	PMTSchemaVersion       = 1
	PMTStateUnread         = "unread"
	PMTStateFetched        = "fetched"
	PMTStateCancelled      = "cancelled"
	PMTStateSuperseded     = "superseded"
	PMTStateExpired        = "expired"
	MaxPMTTitleBytes       = 160
	MaxPMTInstructionBytes = 32 << 10
	MaxPMTQueueEntries     = 64
	MaxPMTReadCount        = 1_000_000
)

// PMT is a Planner Message Token. It is a Local operational entity: its body
// never enters the Hub or the Shared outbox. The target fields are snapshots
// resolved by the server and are never caller-selected authority.
type PMT struct {
	SchemaVersion           int        `json:"schema_version"`
	ID                      string     `json:"id"`
	ProjectID               string     `json:"project_id"`
	ProjectCode             string     `json:"project_code"`
	Title                   string     `json:"title"`
	Instruction             string     `json:"instruction"`
	PlannerSessionID        string     `json:"planner_session_id"`
	TargetSessionID         string     `json:"target_session_id,omitempty"`
	TargetAirelaySessionKey string     `json:"target_airelay_session_key"`
	TargetAgentID           string     `json:"target_agent_id"`
	TrainID                 string     `json:"train_id,omitempty"`
	ItemPosition            int        `json:"item_position,omitempty"`
	TaskID                  string     `json:"task_id,omitempty"`
	AttemptNumber           uint64     `json:"attempt_number,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	State                   string     `json:"state"`
	FirstFetchedAt          *time.Time `json:"first_fetched_at,omitempty"`
	DeliveredAt             *time.Time `json:"delivered_at,omitempty"`
	LastFetchedAt           *time.Time `json:"last_fetched_at,omitempty"`
	CancelledAt             *time.Time `json:"cancelled_at,omitempty"`
	SupersededBy            string     `json:"superseded_by,omitempty"`
	Reference               string     `json:"reference"`
	ReferenceSubmittedAt    *time.Time `json:"reference_submitted_at,omitempty"`
	ReadCount               int        `json:"read_count"`
	ExpiresAt               *time.Time `json:"expires_at,omitempty"`
}

type PMTSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	Order     int       `json:"order"`
}

type PMTQueue struct {
	QueueCount int          `json:"queue_count"`
	Entries    []PMTSummary `json:"entries"`
}

func ValidatePMT(v PMT) error {
	if v.SchemaVersion != PMTSchemaVersion {
		return fmt.Errorf("invalid PMT identity")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return fmt.Errorf("invalid PMT project: %w", err)
	}
	if err := ValidateProjectCode(v.ProjectCode); err != nil || ValidateObjectIdentifier(v.ID) != nil || !validPMTID(v.ID, v.ProjectCode) {
		return fmt.Errorf("invalid PMT project code or id")
	}
	if err := validatePMTText(v.Title, MaxPMTTitleBytes, false, false); err != nil {
		return fmt.Errorf("invalid PMT title: %w", err)
	}
	if err := validatePMTText(v.Instruction, MaxPMTInstructionBytes, true, true); err != nil {
		return fmt.Errorf("invalid PMT instruction: %w", err)
	}
	if v.PlannerSessionID == "" || v.TargetAirelaySessionKey == "" || v.TargetAgentID == "" || v.Reference == "" || v.CreatedAt.IsZero() || v.ReadCount < 0 || v.ReadCount > MaxPMTReadCount {
		return fmt.Errorf("invalid PMT provenance or telemetry")
	}
	if v.State != PMTStateUnread && v.State != PMTStateFetched && v.State != PMTStateCancelled && v.State != PMTStateSuperseded && v.State != PMTStateExpired {
		return fmt.Errorf("invalid PMT state")
	}
	if v.State == PMTStateSuperseded && v.SupersededBy == "" {
		return fmt.Errorf("superseded PMT requires replacement")
	}
	if v.TrainID != "" && ValidateObjectIdentifier(v.TrainID) != nil {
		return fmt.Errorf("invalid PMT Train identity")
	}
	if v.TaskID != "" && ValidateCanonicalTaskID(v.TaskID) != nil {
		return fmt.Errorf("invalid PMT Task identity")
	}
	if v.AttemptNumber > 0 && v.TaskID == "" {
		return fmt.Errorf("PMT Attempt requires Task identity")
	}
	if v.ItemPosition < 0 {
		return fmt.Errorf("PMT item position is invalid")
	}
	return nil
}

func validPMTID(id, projectCode string) bool {
	prefix := projectCode + "-PMT"
	if !strings.HasPrefix(id, prefix) || len(id) == len(prefix) {
		return false
	}
	_, err := strconv.ParseUint(id[len(prefix):], 10, 64)
	return err == nil
}

func validatePMTText(value string, max int, nonEmpty, allowNewline bool) error {
	if !utf8.ValidString(value) || len([]byte(value)) > max || strings.ContainsRune(value, '\x00') || (nonEmpty && strings.TrimSpace(value) == "") {
		return fmt.Errorf("invalid UTF-8 or bounds")
	}
	for _, r := range value {
		if unicode.IsControl(r) && (!allowNewline || (r != '\n' && r != '\r' && r != '\t')) {
			return fmt.Errorf("control character is not allowed")
		}
	}
	if !allowNewline && strings.TrimSpace(value) != value {
		return fmt.Errorf("text must be trimmed")
	}
	return nil
}
