package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type TaskLifecyclePage struct {
	Tasks      []model.TaskAuthoring
	NextCursor string
	HasMore    bool
	CursorKind string
}

func (s *Service) TaskLifecycleCreate(ctx context.Context, in TaskAuthoringCreateInput, operationID string) (model.TaskAuthoring, OperationResult, error) {
	if operationID == "" {
		operationID = "task-lifecycle-create-" + strconv.FormatInt(s.durableNow().UnixNano(), 10)
	}
	return s.taskAuthoringCreateShared(ctx, operationID, in)
}

func (s *Service) TaskLifecycleUpdate(ctx context.Context, in TaskAuthoringUpdateInput) (model.TaskAuthoring, OperationResult, error) {
	return s.taskAuthoringUpdateShared(ctx, "task-lifecycle-update-"+strconv.FormatInt(s.durableNow().UnixNano(), 10), in)
}

func (s *Service) TaskLifecycleRead(ctx context.Context, projectID, taskID string, revision int) (model.TaskAuthoring, error) {
	if revision == 0 {
		return s.TaskAuthoringRead(ctx, projectID, taskID)
	}
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.TaskAuthoring{}, err
	}
	record, err := s.Durability.ReadSharedRevision(ctx, "task", projectID, taskID, int64(revision))
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(record.Payload, &task); err != nil {
		return model.TaskAuthoring{}, err
	}
	if task.ID != taskID || task.ProjectID != projectID || int64(task.Revision) != record.Revision {
		return model.TaskAuthoring{}, fmt.Errorf("task revision identity mismatch")
	}
	return task, model.ValidateTaskAuthoringRevision(task, task.Summary == "")
}

func (s *Service) TaskLifecycleArchive(ctx context.Context, projectID, taskID, actor, reason string) (model.TaskAuthoring, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.TaskAuthoring{}, err
	}
	entity, err := s.Durability.ReadSharedTask(ctx, taskID)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	var current model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		return model.TaskAuthoring{}, err
	}
	if current.ProjectID != projectID || current.ID != taskID {
		return model.TaskAuthoring{}, fmt.Errorf("shared task ownership mismatch")
	}
	if current.Status == model.TaskAuthoringArchived {
		return current, nil
	}
	hadReadySeal := current.ReadySeal != nil
	if admitted, err := s.taskAdmittedToNonterminalTrainShared(ctx, projectID, taskID); err != nil {
		return model.TaskAuthoring{}, err
	} else if admitted {
		return model.TaskAuthoring{}, fmt.Errorf("Task %q is admitted to a nonterminal Train and cannot be archived", taskID)
	}
	current.Status = model.TaskAuthoringArchived
	current.Revision++
	current.UpdatedAt = s.durableNow()
	current.ReadySeal = nil
	current.RevisionSHA256 = ""
	current.RevisionSHA256, err = model.HashTaskAuthoring(current)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	if err := model.ValidateTaskAuthoring(current); err != nil {
		return model.TaskAuthoring{}, err
	}
	payload, err := json.Marshal(current)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	if _, err := s.Durability.CommitSharedLifecycleArchive(ctx, sqlitestore.SharedLifecycleArchive{SharedLifecycleRevision: sqlitestore.SharedLifecycleRevision{
		OperationID: "task-lifecycle-archive-" + strconv.FormatInt(s.durableNow().UnixNano(), 10), EntityType: "task", ProjectID: projectID, EntityID: taskID,
		ExpectedRevision: entity.Revision, ExpectedStoreRevision: entity.Revision, Revision: int64(current.Revision), Payload: payload, Actor: actor, Reason: reason, ChangedFields: taskArchiveChangedFields(hadReadySeal), CreatedAt: s.durableNow(),
	}}); err != nil {
		return model.TaskAuthoring{}, err
	}
	return current, nil
}

// taskLifecycleComplete records successful execution as a normal immutable
// SharedLifecycle Task revision. It deliberately does not use archive: a
// completed Task remains canonical evidence of the integrated work.
func (s *Service) taskLifecycleComplete(ctx context.Context, projectID, taskID, actor, reason string) (model.TaskAuthoring, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.TaskAuthoring{}, err
	}
	entity, err := s.Durability.ReadSharedTask(ctx, taskID)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	var current model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		return model.TaskAuthoring{}, err
	}
	if current.ProjectID != projectID || current.ID != taskID {
		return model.TaskAuthoring{}, fmt.Errorf("shared task ownership mismatch")
	}
	if current.Status == model.TaskAuthoringDone {
		return current, nil
	}
	if current.Status == model.TaskAuthoringArchived {
		return model.TaskAuthoring{}, fmt.Errorf("archived Task cannot be completed")
	}
	if current.Status != model.TaskAuthoringPlanned && current.Status != model.TaskAuthoringReady {
		return model.TaskAuthoring{}, fmt.Errorf("Task is not safely completable from status %q", current.Status)
	}
	hadReadySeal := current.ReadySeal != nil
	current.Status = model.TaskAuthoringDone
	current.Revision++
	current.UpdatedAt = s.durableNow()
	current.ReadySeal = nil
	current.RevisionSHA256 = ""
	current.RevisionSHA256, err = model.HashTaskAuthoring(current)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	if err := model.ValidateTaskAuthoring(current); err != nil {
		return model.TaskAuthoring{}, err
	}
	payload, err := json.Marshal(current)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	changed := []string{"status"}
	if hadReadySeal {
		changed = append(changed, "ready_seal")
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		operationID = "task-lifecycle-complete-" + strconv.FormatInt(s.durableNow().UnixNano(), 10)
	}
	if _, err := s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{
		OperationID: operationID, EntityType: "task", ProjectID: projectID, EntityID: taskID,
		ExpectedRevision: entity.Revision, ExpectedStoreRevision: entity.Revision, Revision: int64(current.Revision),
		Kind: "complete", HistoryMutationKind: "complete", Payload: payload, Actor: actor, Reason: reason,
		ChangedFields: changed, CreatedAt: s.durableNow(),
	}); err != nil {
		return model.TaskAuthoring{}, err
	}
	return current, nil
}

func taskArchiveChangedFields(hadReadySeal bool) []string {
	fields := []string{"status"}
	if hadReadySeal {
		fields = append(fields, "ready_seal")
	}
	return fields
}

func (s *Service) TaskLifecycleListQuery(ctx context.Context, projectID, text, status string, typ model.TaskType, cursor string, includeArchived bool) (TaskLifecyclePage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return TaskLifecyclePage{}, err
	}
	filters := map[string]string{}
	if status != "" {
		filters["status"] = status
	}
	if typ != "" {
		normalized, err := model.NormalizeTaskType(typ)
		if err != nil {
			return TaskLifecyclePage{}, err
		}
		filters["type"] = string(normalized)
	}
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{EntityType: "task", ProjectID: projectID, Text: text, Filters: filters, IncludeArchived: includeArchived || status == model.TaskAuthoringArchived, ExcludeSuperseded: true, Limit: sqlitestore.SharedLifecycleQueryMaxRows, Cursor: cursor})
	if err != nil {
		return TaskLifecyclePage{}, err
	}
	result := TaskLifecyclePage{
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
		Tasks:      make([]model.TaskAuthoring, 0, len(page.Entities)),
	}
	for _, entity := range page.Entities {
		var task model.TaskAuthoring
		if err := json.Unmarshal(entity.Payload, &task); err != nil {
			return TaskLifecyclePage{}, err
		}
		if task.ID != entity.ID || task.ProjectID != projectID {
			return TaskLifecyclePage{}, fmt.Errorf("shared task identity mismatch")
		}
		task.Type = model.DefaultTaskType(task.Type)
		if err := model.ValidateTaskAuthoring(task); err != nil {
			return TaskLifecyclePage{}, err
		}
		result.Tasks = append(result.Tasks, task)
	}
	return result, nil
}

func (s *Service) TaskLifecycleHistory(ctx context.Context, projectID, taskID, cursor string) (sqlitestore.SharedHistoryPage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return sqlitestore.SharedHistoryPage{}, err
	}
	after := int64(0)
	kind := "task-history:" + projectID + ":" + taskID
	if cursor != "" {
		key, err := pagination.DecodeOpaqueKeyset(cursor, kind)
		if err != nil {
			return sqlitestore.SharedHistoryPage{}, fmt.Errorf("invalid task history cursor")
		}
		after, err = strconv.ParseInt(key, 10, 64)
		if err != nil || after < 0 {
			return sqlitestore.SharedHistoryPage{}, fmt.Errorf("invalid task history cursor")
		}
	}
	return s.Durability.ListSharedHistoryPage(ctx, "task", projectID, taskID, after, sqlitestore.SharedLifecycleQueryMaxRows)
}

func taskAuthoringChangedFields(in TaskAuthoringUpdateInput) []string {
	fields := make([]string, 0, 12)
	if in.Type != nil {
		fields = append(fields, "type")
	}
	if in.Scope != nil {
		fields = append(fields, "scope")
	}
	if in.Title != nil {
		fields = append(fields, "title")
	}
	if in.Summary != nil {
		fields = append(fields, "summary")
	}
	if in.Objective != nil {
		fields = append(fields, "objective")
	}
	if in.AcceptanceCriteria != nil {
		fields = append(fields, "acceptance_criteria")
	}
	if in.Constraints != nil {
		fields = append(fields, "constraints")
	}
	if in.Priority != nil {
		fields = append(fields, "priority")
	}
	if in.Dependencies != nil {
		fields = append(fields, "dependencies")
	}
	if in.PreparationReferences != nil {
		fields = append(fields, "preparation_references")
	}
	if in.Metadata != nil {
		fields = append(fields, "metadata")
	}
	if in.ADRRelation != nil {
		fields = append(fields, "adr_relation")
	}
	if in.ADRReferences != nil {
		fields = append(fields, "adr_references")
	}
	if len(fields) == 0 {
		fields = append(fields, "content")
	}
	return fields
}

var _ = strings.TrimSpace
