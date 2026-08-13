package model

import (
	"regexp"
	"time"
)

const (
	OperatorJournalSchemaVersion = 1
	MaxOperatorSummaryBytes      = 4096
	MaxOperatorActorBytes        = 256
	MaxOperatorSessionIDBytes    = 256
	MaxOperatorContentItems      = 128
	MaxOperatorContentItemBytes  = 2048
	MaxOperatorReferenceItems    = 128
	MaxOperatorHistoryLimit      = 200
)

// OperatorJournalNumberPattern is the exact decimal range accepted by the
// compact-ID model: positive integers through JavaScript's safe integer
// maximum, without leading zeroes.
const OperatorJournalNumberPattern = `[1-9][0-9]{0,14}|[1-8][0-9]{15}|900[0-6][0-9]{12}|90070[0-9]{11}|90071[0-8][0-9]{10}|900719[0-8][0-9]{9}|9007199[0-1][0-9]{8}|90071992[0-4][0-9]{7}|900719925[0-3][0-9]{6}|9007199254[0-6][0-9]{5}|90071992547[0-3][0-9]{4}|9007199254740[0-8][0-9]{2}|90071992547409[0-8][0-9]|9007199254740990|9007199254740991`

const OperatorEventIDPattern = `^[A-Z]{3}-(OPR|JRN)(` + OperatorJournalNumberPattern + `)$`

const OperatorHistoricalEventIDPattern = `^[A-Z]{3}-O(` + OperatorJournalNumberPattern + `)$`

const OperatorCompactADRPattern = `^[A-Z]{3}-ADR(` + OperatorJournalNumberPattern + `)$`

const OperatorHistoricalADRPattern = `^[A-Z]{3}-A(` + OperatorJournalNumberPattern + `)$`

var operatorEventIDRE = regexp.MustCompile(`^([A-Z]{3})-OPR(` + OperatorJournalNumberPattern + `)$`)

var historicalOperatorEventIDRE = regexp.MustCompile(`^([A-Z]{3})-O(` + OperatorJournalNumberPattern + `)$`)

type OperatorJournalKind string

const (
	OperatorUserTalk         OperatorJournalKind = "user_talk"
	OperatorReasoningSummary OperatorJournalKind = "reasoning_summary"
	OperatorTaskPlan         OperatorJournalKind = "task_plan"
	OperatorTaskReview       OperatorJournalKind = "task_review"
	OperatorOperation        OperatorJournalKind = "operation"
	OperatorCheckpoint       OperatorJournalKind = "checkpoint"
	OperatorCorrection       OperatorJournalKind = "correction"
)

var operatorJournalKinds = map[OperatorJournalKind]bool{
	OperatorUserTalk: true, OperatorReasoningSummary: true, OperatorTaskPlan: true,
	OperatorTaskReview: true, OperatorOperation: true, OperatorCheckpoint: true,
	OperatorCorrection: true,
}

type OperatorJournalContent struct {
	Decisions   []string `json:"decisions"`
	Commitments []string `json:"commitments"`
	Facts       []string `json:"facts"`
	Assumptions []string `json:"assumptions"`
	Blockers    []string `json:"blockers"`
	Unresolved  []string `json:"unresolved"`
	NextActions []string `json:"next_actions"`
}

type OperatorJournalReferences struct {
	PlanSections []string `json:"plan_sections"`
	ADRs         []string `json:"adrs"`
	Tasks        []string `json:"tasks"`
	Runs         []string `json:"runs"`
	Commits      []string `json:"commits"`
	Identities   []string `json:"identities"`
}

type OperatorJournalCounter struct {
	SchemaVersion   int    `json:"schema_version"`
	ProjectID       string `json:"project_id"`
	NextEventNumber uint64 `json:"next_event_number"`
}

type OperatorJournalEvent struct {
	SchemaVersion     int                       `json:"schema_version"`
	ID                string                    `json:"id"`
	ProjectID         string                    `json:"project_id"`
	SessionID         *string                   `json:"session_id"`
	Kind              OperatorJournalKind       `json:"kind"`
	Summary           string                    `json:"summary"`
	Content           OperatorJournalContent    `json:"content"`
	References        OperatorJournalReferences `json:"references"`
	SupersedesEventID string                    `json:"supersedes_event_id,omitempty"`
	Actor             string                    `json:"actor"`
	OccurredAt        time.Time                 `json:"occurred_at"`
	RecordedAt        time.Time                 `json:"recorded_at"`
}

// Short aliases keep callers concise without introducing another journal model.
type OperatorEvent = OperatorJournalEvent

type OperatorContent = OperatorJournalContent

type OperatorReferences = OperatorJournalReferences
