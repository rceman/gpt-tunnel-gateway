package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const TaskRevisionSchemaVersion = 1

var taskRevisionIDRE = regexp.MustCompile(`^([A-Z]{3}-TSK` + OperatorJournalNumberPattern + `)\.REV([1-9][0-9]*)$`)

// TaskRevision is an immutable, project-scoped revision of one stable Task.
// The original tasks/<id>.json record is revision 1 for legacy tasks; later
// revisions live below the task's revisions directory and never rewrite it.
type TaskRevision struct {
	SchemaVersion          int       `json:"schema_version"`
	ID                     string    `json:"id"`
	TaskID                 string    `json:"task_id"`
	TaskRevision           int       `json:"task_revision"`
	RevisionSHA256         string    `json:"revision_sha256"`
	ParentTaskRevision     int       `json:"parent_task_revision,omitempty"`
	ParentTaskSHA256       string    `json:"parent_task_sha256,omitempty"`
	ProjectID              string    `json:"project_id"`
	Title                  string    `json:"title"`
	Type                   TaskType  `json:"type,omitempty"`
	Objective              string    `json:"objective"`
	Branch                 string    `json:"branch"`
	BaseRevision           string    `json:"base_revision,omitempty"`
	AcceptanceCriteria     []string  `json:"acceptance_criteria"`
	Constraints            []string  `json:"constraints"`
	RequiredGates          []string  `json:"required_gates,omitempty"`
	WorkflowPolicyRevision int       `json:"workflow_policy_revision"`
	OperationClass         string    `json:"operation_class"`
	EffectiveCIField       string    `json:"effective_ci_field"`
	EffectiveCIMode        string    `json:"effective_ci_mode"`
	WaitForCI              bool      `json:"wait_for_ci"`
	CIBlocking             bool      `json:"ci_blocking"`
	AgentMayWait           bool      `json:"agent_may_wait"`
	Status                 string    `json:"status"`
	SourceTrainID          string    `json:"source_train_id,omitempty"`
	SourceItemPosition     int       `json:"source_item_position,omitempty"`
	SourceAttemptNumber    int       `json:"source_attempt_number,omitempty"`
	CreatedBy              string    `json:"created_by"`
	CreatedAt              time.Time `json:"created_at"`
}

type TaskRevisionStatus struct {
	SchemaVersion       int       `json:"schema_version"`
	ID                  string    `json:"id"`
	TaskID              string    `json:"task_id"`
	TaskRevision        int       `json:"task_revision"`
	RevisionSHA256      string    `json:"revision_sha256"`
	ParentTaskRevision  int       `json:"parent_task_revision,omitempty"`
	Status              string    `json:"status"`
	Branch              string    `json:"branch"`
	BaseRevision        string    `json:"base_revision,omitempty"`
	SourceTrainID       string    `json:"source_train_id,omitempty"`
	SourceItemPosition  int       `json:"source_item_position,omitempty"`
	SourceAttemptNumber int       `json:"source_attempt_number,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

func (r TaskRevision) StatusView() TaskRevisionStatus {
	return TaskRevisionStatus{
		SchemaVersion:       r.SchemaVersion,
		ID:                  r.ID,
		TaskID:              r.TaskID,
		TaskRevision:        r.TaskRevision,
		RevisionSHA256:      r.RevisionSHA256,
		ParentTaskRevision:  r.ParentTaskRevision,
		Status:              r.Status,
		Branch:              r.Branch,
		BaseRevision:        r.BaseRevision,
		SourceTrainID:       r.SourceTrainID,
		SourceItemPosition:  r.SourceItemPosition,
		SourceAttemptNumber: r.SourceAttemptNumber,
		CreatedAt:           r.CreatedAt,
	}
}

func TaskRevisionFromTask(task Task) TaskRevision {
	return TaskRevision{
		SchemaVersion:          TaskRevisionSchemaVersion,
		ID:                     FormatTaskRevisionIDUnchecked(task.ID, 1),
		TaskID:                 task.ID,
		TaskRevision:           1,
		RevisionSHA256:         task.SHA256,
		ProjectID:              task.ProjectID,
		Title:                  task.Title,
		Type:                   task.Type,
		Objective:              task.Objective,
		Branch:                 task.Branch,
		BaseRevision:           task.BaseRevision,
		AcceptanceCriteria:     append([]string{}, task.AcceptanceCriteria...),
		Constraints:            append([]string{}, task.Constraints...),
		RequiredGates:          append([]string{}, task.RequiredGates...),
		WorkflowPolicyRevision: task.WorkflowPolicyRevision,
		OperationClass:         task.OperationClass,
		EffectiveCIField:       task.EffectiveCIField,
		EffectiveCIMode:        task.EffectiveCIMode,
		WaitForCI:              task.WaitForCI,
		CIBlocking:             task.CIBlocking,
		AgentMayWait:           task.AgentMayWait,
		Status:                 task.Status,
		CreatedBy:              task.CreatedBy,
		CreatedAt:              task.CreatedAt,
	}
}

func (r TaskRevision) Task() Task {
	return Task{
		SchemaVersion:          SchemaVersion,
		ID:                     r.TaskID,
		SHA256:                 r.RevisionSHA256,
		ProjectID:              r.ProjectID,
		Title:                  r.Title,
		Type:                   r.Type,
		Objective:              r.Objective,
		Branch:                 r.Branch,
		BaseRevision:           r.BaseRevision,
		AcceptanceCriteria:     append([]string{}, r.AcceptanceCriteria...),
		Constraints:            append([]string{}, r.Constraints...),
		RequiredGates:          append([]string{}, r.RequiredGates...),
		WorkflowPolicyRevision: r.WorkflowPolicyRevision,
		OperationClass:         r.OperationClass,
		EffectiveCIField:       r.EffectiveCIField,
		EffectiveCIMode:        r.EffectiveCIMode,
		WaitForCI:              r.WaitForCI,
		CIBlocking:             r.CIBlocking,
		AgentMayWait:           r.AgentMayWait,
		Status:                 r.Status,
		CreatedBy:              r.CreatedBy,
		CreatedAt:              r.CreatedAt,
	}
}

func FormatTaskRevisionID(taskID string, revision int) (string, error) {
	if err := ValidateCanonicalTaskID(taskID); err != nil {
		return "", err
	}
	if revision < 1 || uint64(revision) > MaxSafeInteger {
		return "", fmt.Errorf("invalid task revision")
	}
	return FormatTaskRevisionIDUnchecked(taskID, revision), nil
}

func FormatTaskRevisionIDUnchecked(taskID string, revision int) string {
	return taskID + ".REV" + strconv.Itoa(revision)
}

func ParseTaskRevisionID(value string) (string, int, error) {
	matches := taskRevisionIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid task revision id")
	}
	n, err := strconv.ParseUint(matches[2], 10, 64)
	if err != nil || n == 0 || n > MaxSafeInteger {
		return "", 0, fmt.Errorf("invalid task revision number")
	}
	return matches[1], int(n), nil
}

func ValidateTaskRevisionID(value string) error {
	_, _, err := ParseTaskRevisionID(value)
	return err
}

func FormatTaskRevisionRunID(revisionID string, runNumber uint64) (string, error) {
	if err := ValidateTaskRevisionID(revisionID); err != nil {
		return "", err
	}
	if runNumber == 0 || runNumber > MaxSafeInteger {
		return "", fmt.Errorf("invalid task run number")
	}
	return revisionID + "-RUN" + strconv.FormatUint(runNumber, 10), nil
}

func ParseTaskRevisionRunID(value string) (string, uint64, error) {
	marker := strings.LastIndex(value, "-RUN")
	if marker < 0 {
		return "", 0, fmt.Errorf("invalid task revision run id")
	}
	revisionID := value[:marker]
	if err := ValidateTaskRevisionID(revisionID); err != nil {
		return "", 0, err
	}
	n, err := strconv.ParseUint(value[marker+4:], 10, 64)
	if err != nil || n == 0 || n > MaxSafeInteger {
		return "", 0, fmt.Errorf("invalid task run number")
	}
	return revisionID, n, nil
}

func ValidateTaskRevisionRunID(value string) error {
	_, _, err := ParseTaskRevisionRunID(value)
	return err
}

func HashTaskRevision(revision TaskRevision) (string, error) {
	revision.RevisionSHA256 = ""
	data, err := json.Marshal(revision)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateTaskRevision(v TaskRevision) error {
	if v.SchemaVersion != TaskRevisionSchemaVersion || v.TaskID == "" || v.TaskRevision < 1 {
		return fmt.Errorf("invalid task revision identity")
	}
	wantID, err := FormatTaskRevisionID(v.TaskID, v.TaskRevision)
	if err != nil || v.ID != wantID {
		return fmt.Errorf("invalid task revision id")
	}
	if len(v.RevisionSHA256) != 64 || strings.Trim(v.RevisionSHA256, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid task revision sha256")
	}
	if v.TaskRevision == 1 {
		if v.ParentTaskRevision != 0 || v.ParentTaskSHA256 != "" {
			return fmt.Errorf("revision 1 cannot have a parent")
		}
	} else {
		if v.ParentTaskRevision != v.TaskRevision-1 || len(v.ParentTaskSHA256) != 64 || strings.Trim(v.ParentTaskSHA256, "0123456789abcdef") != "" {
			return fmt.Errorf("invalid task revision parent")
		}
		hash, hashErr := HashTaskRevision(v)
		if hashErr != nil || hash != v.RevisionSHA256 {
			return fmt.Errorf("task revision hash mismatch")
		}
	}
	if err := ValidateTask(v.Task()); err != nil {
		// The revision hash is intentionally not the legacy Task hash. Validate
		// semantic Task fields without asking the legacy hash validator to match.
		if !strings.Contains(err.Error(), "task sha256 mismatch") {
			return err
		}
	}
	if v.CreatedAt.IsZero() {
		return fmt.Errorf("task revision created_at is required")
	}
	return nil
}
