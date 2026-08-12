package train

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// AuthoringDraft is the storage-independent semantic input for one Train v2
// task. The service adapter owns Hub allocation and persistence; this package
// owns the transition rules.
type AuthoringDraft struct {
	Title                 string
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
	Title                 *string
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
	task := model.TaskAuthoring{
		SchemaVersion:         model.TaskAuthoringSchemaVersion,
		ID:                    "AAA-TSK1",
		ProjectID:             "example",
		Revision:              1,
		RevisionSHA256:        strings.Repeat("a", 64),
		Title:                 draft.Title,
		Objective:             draft.Objective,
		AcceptanceCriteria:    cloneStrings(draft.AcceptanceCriteria),
		Constraints:           cloneStrings(draft.Constraints),
		Priority:              draft.Priority,
		Dependencies:          cloneStrings(draft.Dependencies),
		PreparationReferences: cloneStrings(draft.PreparationReferences),
		Metadata:              cloneStringMap(draft.Metadata),
		ADRRelation:           draft.ADRRelation,
		ADRReferences:         cloneStrings(draft.ADRReferences),
		Status:                model.TaskAuthoringPlanned,
		CreatedBy:             "validator",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := model.ValidateTaskAuthoring(task); err != nil && !strings.Contains(err.Error(), "revision hash mismatch") {
		return err
	}
	return nil
}

// NewTask creates and validates a planned task with a derived revision hash.
func NewTask(projectID, taskID string, draft AuthoringDraft, createdBy string, now time.Time) (model.TaskAuthoring, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.TaskAuthoring{}, err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return model.TaskAuthoring{}, err
	}
	if createdBy == "" || strings.ContainsAny(createdBy, "\x00\r\n") {
		return model.TaskAuthoring{}, fmt.Errorf("created_by is required")
	}
	if draft.ADRRelation == "" {
		draft.ADRRelation = model.TaskADRNoRequired
	}
	if err := ValidateDraft(draft); err != nil {
		return model.TaskAuthoring{}, err
	}
	now = now.UTC()
	task := model.TaskAuthoring{
		SchemaVersion:         model.TaskAuthoringSchemaVersion,
		ID:                    taskID,
		ProjectID:             projectID,
		Revision:              1,
		Title:                 draft.Title,
		Objective:             draft.Objective,
		AcceptanceCriteria:    cloneStrings(draft.AcceptanceCriteria),
		Constraints:           cloneStrings(draft.Constraints),
		Priority:              draft.Priority,
		Dependencies:          cloneStrings(draft.Dependencies),
		PreparationReferences: cloneStrings(draft.PreparationReferences),
		Metadata:              cloneStringMap(draft.Metadata),
		ADRRelation:           draft.ADRRelation,
		ADRReferences:         cloneStrings(draft.ADRReferences),
		Status:                model.TaskAuthoringPlanned,
		CreatedBy:             createdBy,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	var err error
	task.RevisionSHA256, err = model.HashTaskAuthoring(task)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return model.TaskAuthoring{}, err
	}
	return task, nil
}

// UpdateTask applies a semantic edit. It returns the original task and false
// when the patch is empty or does not change the semantic content.
func UpdateTask(current model.TaskAuthoring, patch AuthoringPatch, updatedBy string, now time.Time) (model.TaskAuthoring, bool, error) {
	if err := model.ValidateTaskAuthoring(current); err != nil {
		return model.TaskAuthoring{}, false, err
	}
	if current.Status != model.TaskAuthoringPlanned && current.Status != model.TaskAuthoringReady {
		return model.TaskAuthoring{}, false, fmt.Errorf("invalid task authoring status")
	}
	if updatedBy == "" || strings.ContainsAny(updatedBy, "\x00\r\n") {
		return model.TaskAuthoring{}, false, fmt.Errorf("updated_by is required")
	}
	updated := current
	changed := false
	if patch.Title != nil && *patch.Title != updated.Title {
		updated.Title, changed = *patch.Title, true
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
	draft := AuthoringDraft{Title: updated.Title, Objective: updated.Objective, AcceptanceCriteria: updated.AcceptanceCriteria, Constraints: updated.Constraints, Priority: updated.Priority, Dependencies: updated.Dependencies, PreparationReferences: updated.PreparationReferences, Metadata: updated.Metadata, ADRRelation: updated.ADRRelation, ADRReferences: updated.ADRReferences}
	if err := ValidateDraft(draft); err != nil {
		return model.TaskAuthoring{}, false, err
	}
	updated.Revision++
	updated.RevisionSHA256 = ""
	updated.Status = model.TaskAuthoringPlanned
	updated.ReadySeal = nil
	updated.UpdatedAt = now.UTC()
	var err error
	updated.RevisionSHA256, err = model.HashTaskAuthoring(updated)
	if err != nil {
		return model.TaskAuthoring{}, false, err
	}
	if err := model.ValidateTaskAuthoring(updated); err != nil {
		return model.TaskAuthoring{}, false, err
	}
	return updated, true, nil
}

// ReadyTask seals the current revision. Re-sealing an already-ready task is
// intentionally idempotent and preserves its existing seal.
func ReadyTask(current model.TaskAuthoring, readyBy string, readyAt time.Time) (model.TaskAuthoring, error) {
	if err := model.ValidateTaskAuthoring(current); err != nil {
		return model.TaskAuthoring{}, err
	}
	if strings.TrimSpace(readyBy) == "" || strings.ContainsAny(readyBy, "\x00\r\n") {
		return model.TaskAuthoring{}, fmt.Errorf("ready_by is required")
	}
	if current.Status == model.TaskAuthoringReady {
		return current, nil
	}
	current.Status = model.TaskAuthoringReady
	current.ReadySeal = &model.TaskReadySeal{Revision: current.Revision, RevisionSHA256: current.RevisionSHA256, ReadyBy: readyBy, ReadyAt: readyAt.UTC()}
	current.UpdatedAt = readyAt.UTC()
	if err := model.ValidateTaskAuthoring(current); err != nil {
		return model.TaskAuthoring{}, err
	}
	return current, nil
}

func CheckRevision(task model.TaskAuthoring, revision int, hash string) error {
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
