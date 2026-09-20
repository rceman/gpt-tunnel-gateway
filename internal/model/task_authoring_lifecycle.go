package model

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// AuthoringDraft is the storage-independent semantic input for one
// canonical task. The service adapter owns Hub allocation and persistence; this package
// owns the transition rules.
type AuthoringDraft struct {
	Type                  TaskType
	Execution             TaskExecution
	Scope                 *TaskScope
	Title                 string
	Summary               string
	Objective             string
	AcceptanceCriteria    []string
	Constraints           []string
	Priority              string
	Dependencies          []string
	PreparationReferences []string
	Metadata              map[string]string
	ADRRelation           string
	ADRReferences         []string
}

// AuthoringPatch contains only mutable semantic fields. Identity, revision,
// hash and ready-seal fields are always derived here.
type AuthoringPatch struct {
	Type                  *TaskType
	Execution             *TaskExecution
	Scope                 *TaskScope
	Title                 *string
	Summary               *string
	Objective             *string
	AcceptanceCriteria    *[]string
	Constraints           *[]string
	Priority              *string
	Dependencies          *[]string
	PreparationReferences *[]string
	Metadata              *map[string]string
	ADRRelation           *string
	ADRReferences         *[]string
}

// ValidateDraft applies the model's canonical content rules without requiring
// a repository, Hub, or generated task identity.
func ValidateDraft(draft AuthoringDraft) error {
	now := time.Unix(1, 0).UTC()
	task := TaskAuthoring{
		SchemaVersion:         TaskAuthoringSchemaVersion,
		ID:                    "AAA-TSK1",
		ProjectID:             "example",
		Revision:              1,
		Type:                  DefaultTaskType(draft.Type),
		Execution:             draft.Execution,
		Scope:                 draft.Scope,
		RevisionSHA256:        strings.Repeat("a", 64),
		Title:                 draft.Title,
		Summary:               draft.Summary,
		Objective:             draft.Objective,
		AcceptanceCriteria:    cloneStrings(draft.AcceptanceCriteria),
		Constraints:           cloneStrings(draft.Constraints),
		Priority:              draft.Priority,
		Dependencies:          cloneStrings(draft.Dependencies),
		PreparationReferences: cloneStrings(draft.PreparationReferences),
		Metadata:              cloneStringMap(draft.Metadata),
		ADRRelation:           draft.ADRRelation,
		ADRReferences:         cloneStrings(draft.ADRReferences),
		Status:                TaskAuthoringPlanned,
		CreatedBy:             "validator",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := ValidateTaskAuthoring(task); err != nil && !strings.Contains(err.Error(), "revision hash mismatch") {
		return err
	}
	return nil
}

// NewTask creates and validates a planned task with a derived revision hash.
func NewTask(projectID, taskID string, draft AuthoringDraft, createdBy string, now time.Time) (TaskAuthoring, error) {
	if err := ValidateProjectIdentifier(projectID); err != nil {
		return TaskAuthoring{}, err
	}
	if err := ValidateCanonicalTaskID(taskID); err != nil {
		return TaskAuthoring{}, err
	}
	if createdBy == "" || strings.ContainsAny(createdBy, "\x00\r\n") {
		return TaskAuthoring{}, fmt.Errorf("created_by is required")
	}
	if draft.ADRRelation == "" {
		draft.ADRRelation = TaskADRNoRequired
	}
	typ, err := NormalizeTaskType(draft.Type)
	if err != nil {
		return TaskAuthoring{}, err
	}
	draft.Type = typ
	execution, err := NormalizeTaskExecution(draft.Execution)
	if err != nil {
		return TaskAuthoring{}, err
	}
	draft.Execution = execution
	draft.Scope, err = NormalizeTaskScope(draft.Scope)
	if err != nil {
		return TaskAuthoring{}, err
	}
	if err := ValidateDraft(draft); err != nil {
		return TaskAuthoring{}, err
	}
	now = now.UTC()
	task := TaskAuthoring{
		SchemaVersion:         TaskAuthoringSchemaVersion,
		ID:                    taskID,
		ProjectID:             projectID,
		Revision:              1,
		Type:                  draft.Type,
		Execution:             draft.Execution,
		Scope:                 draft.Scope,
		Title:                 draft.Title,
		Summary:               draft.Summary,
		Objective:             draft.Objective,
		AcceptanceCriteria:    cloneStrings(draft.AcceptanceCriteria),
		Constraints:           cloneStrings(draft.Constraints),
		Priority:              draft.Priority,
		Dependencies:          cloneStrings(draft.Dependencies),
		PreparationReferences: cloneStrings(draft.PreparationReferences),
		Metadata:              cloneStringMap(draft.Metadata),
		ADRRelation:           draft.ADRRelation,
		ADRReferences:         cloneStrings(draft.ADRReferences),
		Status:                TaskAuthoringPlanned,
		CreatedBy:             createdBy,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	task.RevisionSHA256, err = HashTaskAuthoring(task)
	if err != nil {
		return TaskAuthoring{}, err
	}
	if err := ValidateTaskAuthoring(task); err != nil {
		return TaskAuthoring{}, err
	}
	return task, nil
}

// UpdateTask applies a semantic edit. It returns the original task and false
// when the patch is empty or does not change the semantic content.
func UpdateTask(current TaskAuthoring, patch AuthoringPatch, updatedBy string, now time.Time) (TaskAuthoring, bool, error) {
	if err := ValidateTaskAuthoring(current); err != nil {
		return TaskAuthoring{}, false, err
	}
	if current.Status != TaskAuthoringPlanned && current.Status != TaskAuthoringReady {
		return TaskAuthoring{}, false, fmt.Errorf("invalid task authoring status")
	}
	if updatedBy == "" || strings.ContainsAny(updatedBy, "\x00\r\n") {
		return TaskAuthoring{}, false, fmt.Errorf("updated_by is required")
	}
	updated := current
	updated.Type = DefaultTaskType(updated.Type)
	updated.Execution = current.Execution
	changed := false
	if patch.Type != nil {
		typ, err := NormalizeTaskType(*patch.Type)
		if err != nil {
			return TaskAuthoring{}, false, err
		}
		if typ != updated.Type {
			updated.Type, changed = typ, true
		}
	}
	if patch.Execution != nil {
		execution, err := NormalizeTaskExecution(*patch.Execution)
		if err != nil {
			return TaskAuthoring{}, false, err
		}
		if execution != updated.Execution {
			updated.Execution, changed = execution, true
		}
	}
	if patch.Scope != nil {
		scope, err := NormalizeTaskScope(patch.Scope)
		if err != nil {
			return TaskAuthoring{}, false, err
		}
		if !reflect.DeepEqual(scope, updated.Scope) {
			updated.Scope, changed = scope, true
		}
	}
	if patch.Title != nil && *patch.Title != updated.Title {
		updated.Title, changed = *patch.Title, true
	}
	if patch.Summary != nil && *patch.Summary != updated.Summary {
		updated.Summary, changed = *patch.Summary, true
	}
	if patch.Objective != nil && *patch.Objective != updated.Objective {
		updated.Objective, changed = *patch.Objective, true
	}
	if patch.AcceptanceCriteria != nil && !reflect.DeepEqual(*patch.AcceptanceCriteria, updated.AcceptanceCriteria) {
		updated.AcceptanceCriteria, changed = cloneStrings(*patch.AcceptanceCriteria), true
	}
	if patch.Constraints != nil && !reflect.DeepEqual(*patch.Constraints, updated.Constraints) {
		updated.Constraints, changed = cloneStrings(*patch.Constraints), true
	}
	if patch.Priority != nil && *patch.Priority != updated.Priority {
		updated.Priority, changed = *patch.Priority, true
	}
	if patch.Dependencies != nil && !reflect.DeepEqual(*patch.Dependencies, updated.Dependencies) {
		updated.Dependencies, changed = cloneStrings(*patch.Dependencies), true
	}
	if patch.PreparationReferences != nil && !reflect.DeepEqual(*patch.PreparationReferences, updated.PreparationReferences) {
		updated.PreparationReferences, changed = cloneStrings(*patch.PreparationReferences), true
	}
	if patch.Metadata != nil && !reflect.DeepEqual(*patch.Metadata, updated.Metadata) {
		updated.Metadata, changed = cloneStringMap(*patch.Metadata), true
	}
	if patch.ADRRelation != nil && *patch.ADRRelation != updated.ADRRelation {
		updated.ADRRelation, changed = *patch.ADRRelation, true
	}
	if patch.ADRReferences != nil && !reflect.DeepEqual(*patch.ADRReferences, updated.ADRReferences) {
		updated.ADRReferences, changed = cloneStrings(*patch.ADRReferences), true
	}
	if !changed {
		return current, false, nil
	}
	draft := AuthoringDraft{
		Type:                  updated.Type,
		Execution:             updated.Execution,
		Scope:                 updated.Scope,
		Title:                 updated.Title,
		Summary:               updated.Summary,
		Objective:             updated.Objective,
		AcceptanceCriteria:    updated.AcceptanceCriteria,
		Constraints:           updated.Constraints,
		Priority:              updated.Priority,
		Dependencies:          updated.Dependencies,
		PreparationReferences: updated.PreparationReferences,
		Metadata:              updated.Metadata,
		ADRRelation:           updated.ADRRelation,
		ADRReferences:         updated.ADRReferences,
	}
	if err := ValidateDraft(draft); err != nil {
		return TaskAuthoring{}, false, err
	}
	updated.Revision++
	updated.RevisionSHA256 = ""
	updated.Status = TaskAuthoringPlanned
	updated.ReadySeal = nil
	updated.UpdatedAt = now.UTC()
	var err error
	updated.RevisionSHA256, err = HashTaskAuthoring(updated)
	if err != nil {
		return TaskAuthoring{}, false, err
	}
	if err := ValidateTaskAuthoring(updated); err != nil {
		return TaskAuthoring{}, false, err
	}
	return updated, true, nil
}

// ReadyTask seals the current revision. Re-sealing an already-ready task is
// intentionally idempotent and preserves its existing seal.
func ReadyTask(current TaskAuthoring, readyBy string, readyAt time.Time) (TaskAuthoring, error) {
	if err := ValidateTaskAuthoring(current); err != nil {
		return TaskAuthoring{}, err
	}
	if strings.TrimSpace(readyBy) == "" || strings.ContainsAny(readyBy, "\x00\r\n") {
		return TaskAuthoring{}, fmt.Errorf("ready_by is required")
	}
	if err := ValidateTaskPriority(current.Priority, true); err != nil {
		return TaskAuthoring{}, err
	}
	if current.Status == TaskAuthoringReady {
		return current, nil
	}
	current.Status = TaskAuthoringReady
	current.ReadySeal = &TaskReadySeal{
		Revision:       current.Revision,
		RevisionSHA256: current.RevisionSHA256,
		ReadyBy:        readyBy,
		ReadyAt:        readyAt.UTC(),
	}
	current.UpdatedAt = readyAt.UTC()
	if err := ValidateTaskAuthoring(current); err != nil {
		return TaskAuthoring{}, err
	}
	return current, nil
}

// ValidateExecutionTask is the exact readiness boundary used immediately
// before a Task becomes a dispatchable execution.
func ValidateExecutionTask(task TaskAuthoring) error {
	if err := ValidateTaskAuthoring(task); err != nil {
		return err
	}
	if task.Status != TaskAuthoringReady || task.ReadySeal == nil || task.ReadySeal.Revision != task.Revision || task.ReadySeal.RevisionSHA256 != task.RevisionSHA256 {
		return fmt.Errorf("task %q is not ready for execution", task.ID)
	}
	return nil
}

func CheckRevision(task TaskAuthoring, revision int, hash string) error {
	if task.Revision != revision {
		return fmt.Errorf("task authoring revision conflict: expected %d, current %d", revision, task.Revision)
	}
	if hash != "" && task.RevisionSHA256 != hash {
		return fmt.Errorf("task authoring revision hash conflict")
	}
	return nil
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
