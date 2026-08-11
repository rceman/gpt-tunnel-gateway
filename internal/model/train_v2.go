package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	TrainV2SchemaVersion       = 1
	MaxTrainV2Items            = 32
	TrainV2Planned             = "planned"
	TrainV2Running             = "running"
	TrainV2Paused              = "paused"
	TrainV2Blocked             = "blocked"
	TrainV2ReadyForIntegration = "ready_for_integration"
	TrainV2Completed           = "completed"
	TrainV2ItemQueued          = "queued"
	TrainV2ItemRunning         = "running"
	TrainV2ItemFinalized       = "finalized"
	TrainV2ItemReviewed        = "reviewed"
	TrainV2ItemBlocked         = "blocked"
	TrainV2StartSchemaVersion  = 1
	TrainV2StartActive         = "active"
)

var trainV2SHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TrainV2Item is the immutable admission snapshot for one ready Task. It has
// no repository, branch, worktree, Agent, or session identity; those belong to
// later train-start execution state.
type TrainV2Item struct {
	Position           int                         `json:"position"`
	TaskID             string                      `json:"task_id"`
	TaskRevision       int                         `json:"task_revision"`
	TaskRevisionSHA256 string                      `json:"task_revision_sha256"`
	Status             string                      `json:"status"`
	AddedAt            time.Time                   `json:"added_at"`
	RunID              string                      `json:"run_id,omitempty"`
	AgentID            string                      `json:"agent_id,omitempty"`
	StartHead          string                      `json:"start_head,omitempty"`
	Proof              *TrainV2ImplementationProof `json:"proof,omitempty"`
	Review             *TrainV2ItemReview          `json:"review,omitempty"`
}

type TrainV2ImplementationProof struct {
	CheckpointHead    string                 `json:"checkpoint_head"`
	ImplementationSHA string                 `json:"implementation_sha"`
	ReportID          string                 `json:"report_id"`
	GateResults       []CompletionGateResult `json:"gate_results"`
	RecordedAt        time.Time              `json:"recorded_at"`
}

type TrainV2ItemReview struct {
	Outcome    string    `json:"outcome"`
	ReportID   string    `json:"report_id"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

// TrainV2 is a non-running, ordered execution admission record. A later
// train/start transition owns Git and Agent execution identity.
type TrainV2 struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	Revision      int               `json:"revision"`
	Items         []TrainV2Item     `json:"items"`
	Status        string            `json:"status"`
	CreatedBy     string            `json:"created_by"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	FullProof     *TrainV2FullProof `json:"full_proof,omitempty"`
}

type TrainV2FullProof struct {
	CandidateHead string                 `json:"candidate_head"`
	GateResults   []CompletionGateResult `json:"gate_results"`
	RecordedAt    time.Time              `json:"recorded_at"`
}

// TrainV2StartRecord is the portable, safe execution checkpoint. Host-local
// WorktreePath and SessionKey deliberately do not appear here.
type TrainV2StartRecord struct {
	SchemaVersion             int       `json:"schema_version"`
	ProjectID                 string    `json:"project_id"`
	TrainID                   string    `json:"train_id"`
	Status                    string    `json:"status"`
	IntegrationBranch         string    `json:"integration_branch"`
	BaseRevision              string    `json:"base_revision"`
	LaneBranch                string    `json:"lane_branch"`
	RunID                     string    `json:"run_id"`
	CurrentTaskID             string    `json:"current_task_id"`
	CurrentTaskRevision       int       `json:"current_task_revision"`
	CurrentTaskRevisionSHA256 string    `json:"current_task_revision_sha256"`
	StartedAt                 time.Time `json:"started_at"`
}

func ValidateTrainV2StartRecord(v TrainV2StartRecord) error {
	if v.SchemaVersion != TrainV2StartSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Status != TrainV2StartActive || ValidateBranch(v.IntegrationBranch) != nil || !shaRE.MatchString(v.BaseRevision) || ValidateBranch(v.LaneBranch) != nil || ValidateCanonicalRunID(v.RunID) != nil || ValidateCanonicalTaskID(v.CurrentTaskID) != nil || v.CurrentTaskRevision < 1 || !trainV2SHA256RE.MatchString(v.CurrentTaskRevisionSHA256) || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid train v2 start record")
	}
	if _, _, err := ParseTrainV2ID(v.TrainID); err != nil {
		return fmt.Errorf("invalid train v2 start train ID")
	}
	if taskID, _, err := ParseRunID(v.RunID); err != nil || taskID != v.CurrentTaskID {
		return fmt.Errorf("train v2 start run does not bind current task")
	}
	return nil
}

func ValidateTrainV2(v TrainV2) error {
	code, number, err := ParseTrainV2ID(v.ID)
	if err != nil || code == "" || number < 1 || v.SchemaVersion != TrainV2SchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Revision < 1 || v.CreatedBy == "" || strings.ContainsAny(v.CreatedBy, "\x00\r\n") || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid train v2 identity")
	}
	switch v.Status {
	case TrainV2Planned, TrainV2Running, TrainV2Paused, TrainV2Blocked, TrainV2ReadyForIntegration, TrainV2Completed:
	default:
		return fmt.Errorf("invalid train v2 status")
	}
	if len(v.Items) < 1 || len(v.Items) > MaxTrainV2Items {
		return fmt.Errorf("invalid train v2 item count")
	}
	if v.FullProof != nil {
		if err := validateTrainV2FullProof(*v.FullProof); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for position, item := range v.Items {
		if item.Position != position || ValidateCanonicalTaskID(item.TaskID) != nil || item.TaskRevision < 1 || !trainV2SHA256RE.MatchString(item.TaskRevisionSHA256) || item.AddedAt.IsZero() {
			return fmt.Errorf("invalid train v2 item %d", position)
		}
		if err := validateTrainV2ItemExecution(item); err != nil {
			return fmt.Errorf("item %d: %w", position, err)
		}
		if seen[item.TaskID] {
			return fmt.Errorf("duplicate train v2 task %q", item.TaskID)
		}
		seen[item.TaskID] = true
	}
	return nil
}

func validateTrainV2FullProof(proof TrainV2FullProof) error {
	if !shaRE.MatchString(proof.CandidateHead) || proof.RecordedAt.IsZero() || len(proof.GateResults) == 0 {
		return fmt.Errorf("invalid train v2 full proof")
	}
	return ValidateServerGateEvidence(proof.GateResults)
}

func validateTrainV2ItemExecution(item TrainV2Item) error {
	switch item.Status {
	case TrainV2ItemQueued:
		if item.RunID != "" || item.AgentID != "" || item.StartHead != "" || item.Proof != nil || item.Review != nil {
			return fmt.Errorf("queued item has execution state")
		}
	case TrainV2ItemRunning:
		if ValidateCanonicalRunID(item.RunID) != nil || ValidateObjectIdentifier(item.AgentID) != nil || !shaRE.MatchString(item.StartHead) || item.Proof != nil || item.Review != nil {
			return fmt.Errorf("running item has invalid execution state")
		}
	case TrainV2ItemFinalized, TrainV2ItemReviewed, TrainV2ItemBlocked:
		if ValidateCanonicalRunID(item.RunID) != nil || ValidateObjectIdentifier(item.AgentID) != nil || !shaRE.MatchString(item.StartHead) || item.Proof == nil {
			return fmt.Errorf("finalized item has incomplete execution proof")
		}
		if err := validateTrainV2ImplementationProof(*item.Proof); err != nil {
			return err
		}
		if item.Status == TrainV2ItemFinalized && item.Review != nil {
			return fmt.Errorf("unreviewed item has review state")
		}
		if item.Status != TrainV2ItemFinalized && item.Review == nil {
			return fmt.Errorf("reviewed or blocked item has no review state")
		}
		if item.Review != nil {
			if err := ValidateReviewOutcome(item.Review.Outcome); err != nil {
				return err
			}
			if item.Review.ReportID == "" || item.Review.ReviewedAt.IsZero() {
				return fmt.Errorf("invalid item review identity")
			}
		}
	default:
		return fmt.Errorf("invalid train v2 item status")
	}
	return nil
}

func validateTrainV2ImplementationProof(proof TrainV2ImplementationProof) error {
	if !shaRE.MatchString(proof.CheckpointHead) || !shaRE.MatchString(proof.ImplementationSHA) || proof.ReportID == "" || proof.RecordedAt.IsZero() {
		return fmt.Errorf("invalid train v2 implementation proof")
	}
	if err := ValidateServerGateEvidence(proof.GateResults); err != nil {
		return err
	}
	return nil
}
