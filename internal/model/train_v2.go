package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	TrainV2SchemaVersion      = 1
	MaxTrainV2Items           = 32
	TrainV2Planned            = "planned"
	TrainV2ItemQueued         = "queued"
	TrainV2StartSchemaVersion = 1
	TrainV2StartActive        = "active"
)

var trainV2SHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TrainV2Item is the immutable admission snapshot for one ready Task. It has
// no repository, branch, worktree, Agent, or session identity; those belong to
// later train-start execution state.
type TrainV2Item struct {
	Position           int       `json:"position"`
	TaskID             string    `json:"task_id"`
	TaskRevision       int       `json:"task_revision"`
	TaskRevisionSHA256 string    `json:"task_revision_sha256"`
	Status             string    `json:"status"`
	AddedAt            time.Time `json:"added_at"`
}

// TrainV2 is a non-running, ordered execution admission record. A later
// train/start transition owns Git and Agent execution identity.
type TrainV2 struct {
	SchemaVersion int           `json:"schema_version"`
	ID            string        `json:"id"`
	ProjectID     string        `json:"project_id"`
	Revision      int           `json:"revision"`
	Items         []TrainV2Item `json:"items"`
	Status        string        `json:"status"`
	CreatedBy     string        `json:"created_by"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
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
	if v.Status != TrainV2Planned {
		return fmt.Errorf("invalid train v2 status")
	}
	if len(v.Items) < 1 || len(v.Items) > MaxTrainV2Items {
		return fmt.Errorf("invalid train v2 item count")
	}
	seen := map[string]bool{}
	for position, item := range v.Items {
		if item.Position != position || ValidateCanonicalTaskID(item.TaskID) != nil || item.TaskRevision < 1 || !trainV2SHA256RE.MatchString(item.TaskRevisionSHA256) || item.Status != TrainV2ItemQueued || item.AddedAt.IsZero() {
			return fmt.Errorf("invalid train v2 item %d", position)
		}
		if seen[item.TaskID] {
			return fmt.Errorf("duplicate train v2 task %q", item.TaskID)
		}
		seen[item.TaskID] = true
	}
	return nil
}
