package service

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	progressTailLines       = 4
	maxOperationalEvents    = 256
	maxOperationalEventFile = 64 << 10
	resumeObservationWindow = 30 * time.Second
	maxProgressWarnings     = 16
	maxProgressWarningBytes = 256
)

var questionRE = regexp.MustCompile(`(?i)(\?|waiting for (your )?input|need(s)? (your )?input|please (choose|confirm|provide|answer)|which (option|approach|path))`)

// ProgressTask is the bounded task projection used by project_status.
type ProgressTask struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ProgressRun is the bounded run projection used by project_status.  It does
// not contain a session key, dispatch output, or local completion path.
type ProgressRun struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	Status       string     `json:"status"`
	Branch       string     `json:"branch"`
	BaseRevision string     `json:"base_revision"`
	CreatedAt    time.Time  `json:"created_at"`
	DispatchedAt *time.Time `json:"dispatched_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// ProjectProgress is the single bounded progress snapshot returned with
// project_status.  Reads do not write operational events or send prompts.
type ProjectProgress struct {
	LatestTask                       *ProgressTask `json:"latest_task,omitempty"`
	LatestRun                        *ProgressRun  `json:"latest_run,omitempty"`
	AgentState                       string        `json:"agent_state"`
	ControllerReachable              bool          `json:"controller_reachable"`
	AirelayVersion                   string        `json:"airelay_version,omitempty"`
	ProtocolVersion                  string        `json:"protocol_version,omitempty"`
	CapacityWarnings                 []string      `json:"capacity_warnings"`
	ExitCode                         int           `json:"exit_code"`
	Error                            string        `json:"error,omitempty"`
	LastMeaningfulActivity           *time.Time    `json:"last_meaningful_activity,omitempty"`
	LastMeaningfulActivityAgeSeconds int64         `json:"last_meaningful_activity_age_seconds"`
	Tail                             string        `json:"tail"`
	BlockerClassification            string        `json:"blocker_classification"`
	RecommendedNextAction            string        `json:"recommended_next_action"`
	ComponentErrors                  []string      `json:"component_errors"`
}

func (p ProjectProgress) MarshalJSON() ([]byte, error) {
	type alias ProjectProgress
	if p.CapacityWarnings == nil {
		p.CapacityWarnings = []string{}
	}
	if p.AgentState == "" {
		p.AgentState = model.AgentStateUnknown
	}
	if p.ComponentErrors == nil {
		p.ComponentErrors = []string{}
	}
	return json.Marshal(alias(p))
}

type compactionObservation struct {
	Detected        bool
	Started         bool
	Completed       bool
	EventID         string
	Marker          string
	MeaningfulAfter bool
	QuestionAfter   bool
	TailDigest      string
}

type progressEvidence struct {
	Status          airelay.SessionStatus
	StatusError     error
	Tail            string
	TailError       error
	Events          []model.RunOperationalEvent
	Compaction      compactionObservation
	ActiveRun       *model.Run
	ActiveTask      *model.Task
	TaskState       *model.TaskState
	Completion      bool
	LatestTask      *model.Task
	LatestTaskState *model.TaskState
	LatestRun       *model.Run
	ComponentErrors []string
}

func appendComponentError(errors *[]string, name string, err error) {
	if err == nil {
		return
	}
	appendComponentCode(errors, name+"_unavailable")
}

func appendComponentCode(errors *[]string, code string) {
	for _, existing := range *errors {
		if existing == code {
			return
		}
	}
	*errors = append(*errors, code)
}

func hasComponentError(e progressEvidence, names ...string) bool {
	for _, want := range names {
		for _, got := range e.ComponentErrors {
			if got == want+"_unavailable" {
				return true
			}
		}
	}
	return false
}

func (s *Service) operationalEventPath(runID string) string {
	return filepath.Join(s.localRunDir(runID), "events.jsonl")
}

func eventLockName(runID string) string {
	digest := sha256.Sum256([]byte(runID))
	return "events-" + hex.EncodeToString(digest[:8])
}

func digestText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Service) readOperationalEvents(runID string) ([]model.RunOperationalEvent, error) {
	path := s.operationalEventPath(runID)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []model.RunOperationalEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > maxOperationalEventFile {
		return nil, fmt.Errorf("operational event log exceeds bound")
	}
	events := []model.RunOperationalEvent{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if len(events) >= maxOperationalEvents {
			return nil, fmt.Errorf("operational event count exceeds bound")
		}
		var event model.RunOperationalEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("invalid operational event: %w", err)
		}
		if err := model.ValidateRunOperationalEvent(event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
