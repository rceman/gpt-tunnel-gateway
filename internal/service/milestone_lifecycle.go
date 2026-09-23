package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type MilestoneTaskProjection struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
	Archived bool   `json:"-"`
}

type MilestoneView struct {
	Milestone model.Milestone
	Tasks     []MilestoneTaskProjection
	Report    string
}

type MilestonePage struct {
	Milestones []MilestoneView
	NextCursor string
	HasMore    bool
	CursorKind string
}

type MilestoneCreateInput struct {
	ProjectID string
	Title     string
	Summary   string
	Tasks     []string
	CreatedBy string
}

type MilestoneUpdateInput struct {
	ProjectID        string
	Key              string
	Title            *string
	Summary          *string
	Tasks            []string
	ExpectedRevision int
	UpdatedBy        string
	Reason           string
}

type MilestoneCompletionInput struct {
	ProjectID    string
	Key          string
	Evidence     string
	EvidenceRefs []string
	Actor        string
}

func (s *Service) MilestoneLifecycleCreate(ctx context.Context, in MilestoneCreateInput, operationID string) (model.Milestone, OperationResult, error) {
	if operationID == "" {
		operationID = durableMutationOperationID(ctx)
	}
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if operationID == "" {
		encoded, err := json.Marshal(in)
		if err != nil {
			return model.Milestone{}, OperationResult{}, err
		}
		digest := sha256.Sum256(encoded)
		operationID = "milestone-create-" + hex.EncodeToString(digest[:])
	}
	code, err := s.sharedTaskProjectCode(ctx, in.ProjectID)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if err := s.validateMilestoneTasks(ctx, in.ProjectID, code, in.Tasks); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	now := s.durableNow().UTC()
	var created model.Milestone
	_, id, payload, err := s.Durability.CommitSharedLifecycleCreate(ctx, sqlitestore.SharedLifecycleCreate{
		OperationID:         operationID,
		EntityType:          "milestone",
		ProjectID:           in.ProjectID,
		ProjectCode:         code,
		InitialNextNumber:   1,
		Kind:                "milestone-create",
		HistoryMutationKind: "create",
		Actor:               in.CreatedBy,
		Reason:              "create",
		ChangedFields:       []string{"title", "summary", "tasks"},
		CreatedAt:           now,
		BuildPayload: func(id string) ([]byte, error) {
			var err error
			created, err = model.NewMilestone(in.ProjectID, id, in.Title, in.Summary, in.Tasks, in.CreatedBy, now)
			if err != nil {
				return nil, err
			}
			return json.Marshal(created)
		},
	})
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if err := json.Unmarshal(payload, &created); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	return created, OperationResult{
		OperationID: operationID,
		ProjectID:   created.ProjectID,
		EntityKey:   id,
		Revision:    1,
		Status:      created.Status,
	}, nil
}

func (s *Service) MilestoneLifecycleRead(ctx context.Context, projectID, key string, revision int) (model.Milestone, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.Milestone{}, err
	}
	if revision < 0 {
		return model.Milestone{}, fmt.Errorf("invalid milestone revision")
	}
	var payload []byte
	if revision == 0 {
		entity, err := s.Durability.ReadSharedEntity(ctx, "milestone", key)
		if err != nil {
			return model.Milestone{}, err
		}
		payload = entity.Payload
	} else {
		record, err := s.Durability.ReadSharedRevision(ctx, "milestone", projectID, key, int64(revision))
		if err != nil {
			return model.Milestone{}, err
		}
		payload = record.Payload
	}
	var milestone model.Milestone
	if err := json.Unmarshal(payload, &milestone); err != nil {
		return model.Milestone{}, fmt.Errorf("decode shared milestone %s: %w", key, err)
	}
	if milestone.ID != key || milestone.ProjectID != projectID {
		return model.Milestone{}, fmt.Errorf("shared milestone ownership mismatch")
	}
	if err := model.ValidateMilestone(milestone); err != nil {
		return model.Milestone{}, err
	}
	return milestone, nil
}

func (s *Service) MilestoneLifecycleView(ctx context.Context, projectID, key string, revision int) (MilestoneView, error) {
	milestone, err := s.MilestoneLifecycleRead(ctx, projectID, key, revision)
	if err != nil {
		return MilestoneView{}, err
	}
	return s.milestoneView(ctx, milestone)
}

func (s *Service) MilestoneLifecycleUpdate(ctx context.Context, in MilestoneUpdateInput) (model.Milestone, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if in.UpdatedBy == "" || in.Reason == "" || in.ExpectedRevision < 1 {
		return model.Milestone{}, OperationResult{}, fmt.Errorf("milestone update requires actor, reason, and expected revision")
	}
	if in.Tasks != nil {
		return model.Milestone{}, OperationResult{}, fmt.Errorf("milestone update is metadata-only; tasks membership uses append_task or remove_task")
	}
	current, err := s.MilestoneLifecycleRead(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if current.Status == model.MilestoneCompleted || current.Status == model.MilestoneArchived {
		return model.Milestone{}, OperationResult{}, fmt.Errorf("milestone %q cannot be updated from status %q", in.Key, current.Status)
	}
	if current.Revision != in.ExpectedRevision {
		return model.Milestone{}, OperationResult{}, fmt.Errorf("milestone revision conflict: expected %d, current %d", in.ExpectedRevision, current.Revision)
	}
	if in.Title == nil && in.Summary == nil {
		return current, OperationResult{
			ProjectID: current.ProjectID,
			EntityKey: current.ID,
			Revision:  current.Revision,
			Status:    current.Status,
		}, nil
	}
	updated := current
	if in.Title != nil {
		updated.Title = *in.Title
	}
	if in.Summary != nil {
		updated.Summary = *in.Summary
	}
	updated.Revision++
	updated.UpdatedBy = in.UpdatedBy
	updated.UpdatedAt = s.durableNow().UTC()
	if !updated.UpdatedAt.After(current.UpdatedAt) {
		updated.UpdatedAt = current.UpdatedAt.Add(time.Nanosecond)
	}
	if err := model.ValidateMilestone(updated); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "milestone", in.Key)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	operationID := "milestone-update-" + in.Key + "-r" + strconv.Itoa(updated.Revision)
	changed := make([]string, 0, 2)
	if in.Title != nil {
		changed = append(changed, "title")
	}
	if in.Summary != nil {
		changed = append(changed, "summary")
	}
	if _, err := s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{OperationID: operationID, EntityType: "milestone", ProjectID: in.ProjectID, EntityID: in.Key, ExpectedRevision: int64(in.ExpectedRevision), ExpectedStoreRevision: entity.Revision, Revision: int64(updated.Revision), Kind: "milestone-update", HistoryMutationKind: "update", Payload: payload, Actor: in.UpdatedBy, Reason: in.Reason, ChangedFields: changed, CreatedAt: updated.UpdatedAt}); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	return updated, OperationResult{
		OperationID: operationID,
		ProjectID:   in.ProjectID,
		EntityKey:   updated.ID,
		Revision:    updated.Revision,
		Status:      updated.Status,
	}, nil
}

func (s *Service) MilestoneLifecycleActivate(ctx context.Context, projectID, key, actor string) (model.Milestone, error) {
	return s.milestoneStatusTransition(ctx, projectID, key, actor, model.MilestoneActive, "milestone-activate", "activate", "activate")
}

func (s *Service) MilestoneLifecycleComplete(ctx context.Context, in MilestoneCompletionInput) (model.Milestone, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Milestone{}, err
	}
	if in.Actor == "" {
		return model.Milestone{}, fmt.Errorf("milestone completion actor is required")
	}
	if err := model.ValidateMilestoneEvidence(in.ProjectID, in.Evidence, in.EvidenceRefs); err != nil {
		return model.Milestone{}, err
	}
	current, err := s.MilestoneLifecycleRead(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Milestone{}, err
	}
	if current.Status != model.MilestoneActive {
		return model.Milestone{}, fmt.Errorf("milestone %q cannot be completed from status %q", in.Key, current.Status)
	}
	if err := s.requireMilestoneHasNoNonterminalTracks(ctx, current.ProjectID, current.ID); err != nil {
		return model.Milestone{}, err
	}
	if err := s.requireMilestoneTasksDone(ctx, current); err != nil {
		return model.Milestone{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "milestone", in.Key)
	if err != nil {
		return model.Milestone{}, err
	}
	updated := current
	updated.Status = model.MilestoneCompleted
	updated.CompletionEvidence = in.Evidence
	updated.CompletionEvidenceRefs = append([]string{}, in.EvidenceRefs...)
	updated.Revision++
	updated.UpdatedBy = in.Actor
	updated.UpdatedAt = s.durableNow().UTC()
	if !updated.UpdatedAt.After(current.UpdatedAt) {
		updated.UpdatedAt = current.UpdatedAt.Add(time.Nanosecond)
	}
	if err := model.ValidateMilestone(updated); err != nil {
		return model.Milestone{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.Milestone{}, err
	}
	op := "milestone-complete-" + in.Key + "-r" + strconv.Itoa(updated.Revision)
	if _, err := s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{OperationID: op, EntityType: "milestone", ProjectID: in.ProjectID, EntityID: in.Key, ExpectedRevision: int64(current.Revision), ExpectedStoreRevision: entity.Revision, Revision: int64(updated.Revision), Kind: "milestone-complete", HistoryMutationKind: "complete", Payload: payload, Actor: in.Actor, Reason: "completion evidence supplied", ChangedFields: []string{"status", "completion_evidence", "completion_evidence_refs"}, CreatedAt: updated.UpdatedAt}); err != nil {
		return model.Milestone{}, err
	}
	return updated, nil
}

func (s *Service) MilestoneLifecycleArchive(ctx context.Context, projectID, key, actor, reason string) (model.Milestone, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.Milestone{}, err
	}
	current, err := s.MilestoneLifecycleRead(ctx, projectID, key, 0)
	if err != nil {
		return model.Milestone{}, err
	}
	if current.Status == model.MilestoneArchived {
		return current, nil
	}
	if current.Status != model.MilestoneCompleted {
		return model.Milestone{}, fmt.Errorf("milestone %q cannot be archived from status %q", key, current.Status)
	}
	if err := s.requireMilestoneHasNoNonterminalTracks(ctx, current.ProjectID, current.ID); err != nil {
		return model.Milestone{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "milestone", key)
	if err != nil {
		return model.Milestone{}, err
	}
	updated := current
	updated.Status = model.MilestoneArchived
	updated.Revision++
	updated.UpdatedBy = actor
	updated.UpdatedAt = s.durableNow().UTC()
	if !updated.UpdatedAt.After(current.UpdatedAt) {
		updated.UpdatedAt = current.UpdatedAt.Add(time.Nanosecond)
	}
	if err := model.ValidateMilestone(updated); err != nil {
		return model.Milestone{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.Milestone{}, err
	}
	op := "milestone-archive-" + key + "-r" + strconv.Itoa(updated.Revision)
	if _, err := s.Durability.CommitSharedLifecycleArchive(ctx, sqlitestore.SharedLifecycleArchive{SharedLifecycleRevision: sqlitestore.SharedLifecycleRevision{OperationID: op, EntityType: "milestone", ProjectID: projectID, EntityID: key, ExpectedRevision: int64(current.Revision), ExpectedStoreRevision: entity.Revision, Revision: int64(updated.Revision), Payload: payload, Actor: actor, Reason: reason, ChangedFields: []string{"status"}, CreatedAt: updated.UpdatedAt}}); err != nil {
		return model.Milestone{}, err
	}
	return updated, nil
}

func (s *Service) MilestoneLifecycleListQuery(ctx context.Context, projectID, text, status, cursor string, includeArchived bool) (MilestonePage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return MilestonePage{}, err
	}
	filters := map[string]string{}
	if status != "" {
		filters["status"] = status
	}
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{EntityType: "milestone", ProjectID: projectID, Text: text, Filters: filters, IncludeArchived: includeArchived || status == model.MilestoneArchived, Limit: sqlitestore.SharedLifecycleQueryMaxRows, Cursor: cursor})
	if err != nil {
		return MilestonePage{}, err
	}
	result := MilestonePage{
		Milestones: make([]MilestoneView, 0, len(page.Entities)),
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
	}
	for _, entity := range page.Entities {
		var milestone model.Milestone
		if err := json.Unmarshal(entity.Payload, &milestone); err != nil {
			return MilestonePage{}, err
		}
		if milestone.ID != entity.ID || milestone.ProjectID != projectID {
			return MilestonePage{}, fmt.Errorf("shared milestone identity mismatch")
		}
		if err := model.ValidateMilestone(milestone); err != nil {
			return MilestonePage{}, err
		}
		view, err := s.milestoneView(ctx, milestone)
		if err != nil {
			return MilestonePage{}, err
		}
		result.Milestones = append(result.Milestones, view)
	}
	return result, nil
}

func (s *Service) MilestoneLifecycleHistory(ctx context.Context, projectID, key, cursor string) (sqlitestore.SharedHistoryPage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return sqlitestore.SharedHistoryPage{}, err
	}
	after := sqlitestore.SharedLifecycleHistoryCursor{}
	kind := "milestone-history:" + projectID + ":" + key
	if cursor != "" {
		decoded, err := pagination.DecodeOpaqueKeyset(cursor, kind)
		if err != nil {
			return sqlitestore.SharedHistoryPage{}, fmt.Errorf("invalid milestone history cursor")
		}
		after, err = sqlitestore.DecodeSharedLifecycleHistoryCursor(decoded)
		if err != nil {
			return sqlitestore.SharedHistoryPage{}, fmt.Errorf("invalid milestone history cursor")
		}
	}
	return s.Durability.ListSharedLifecycleHistoryPage(ctx, "milestone", projectID, key, after, sqlitestore.SharedLifecycleQueryMaxRows)
}

func (s *Service) milestoneStatusTransition(ctx context.Context, projectID, key, actor, target, kind, historyKind, reason string) (model.Milestone, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.Milestone{}, err
	}
	if actor == "" {
		return model.Milestone{}, fmt.Errorf("milestone transition actor is required")
	}
	current, err := s.MilestoneLifecycleRead(ctx, projectID, key, 0)
	if err != nil {
		return model.Milestone{}, err
	}
	if current.Status != model.MilestonePlanned || target != model.MilestoneActive {
		return model.Milestone{}, fmt.Errorf("milestone %q cannot transition from status %q to %q", key, current.Status, target)
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "milestone", key)
	if err != nil {
		return model.Milestone{}, err
	}
	updated := current
	updated.Status = target
	updated.Revision++
	updated.UpdatedBy = actor
	updated.UpdatedAt = s.durableNow().UTC()
	if !updated.UpdatedAt.After(current.UpdatedAt) {
		updated.UpdatedAt = current.UpdatedAt.Add(time.Nanosecond)
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.Milestone{}, err
	}
	op := kind + "-" + key + "-r" + strconv.Itoa(updated.Revision)
	if _, err := s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{OperationID: op, EntityType: "milestone", ProjectID: projectID, EntityID: key, ExpectedRevision: int64(current.Revision), ExpectedStoreRevision: entity.Revision, Revision: int64(updated.Revision), Kind: kind, HistoryMutationKind: historyKind, Payload: payload, Actor: actor, Reason: reason, ChangedFields: []string{"status"}, CreatedAt: updated.UpdatedAt}); err != nil {
		return model.Milestone{}, err
	}
	return updated, nil
}

func (s *Service) validateMilestoneTasks(ctx context.Context, projectID, projectCode string, taskIDs []string) error {
	if len(taskIDs) > model.MilestoneMaxTasks {
		return fmt.Errorf("milestone task membership exceeds %d", model.MilestoneMaxTasks)
	}
	seen := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		if err := model.ValidateTaskIDForProject(taskID, projectCode); err != nil {
			return fmt.Errorf("invalid milestone task %q: %w", taskID, err)
		}
		if _, exists := seen[taskID]; exists {
			return fmt.Errorf("duplicate milestone task %q", taskID)
		}
		if _, err := s.readSharedTask(ctx, projectID, taskID); err != nil {
			return fmt.Errorf("milestone task %q does not resolve: %w", taskID, err)
		}
		seen[taskID] = struct{}{}
	}
	return nil
}

func (s *Service) requireMilestoneTasksDone(ctx context.Context, milestone model.Milestone) error {
	if len(milestone.Tasks) == 0 {
		return fmt.Errorf("milestone %q cannot complete without Task membership", milestone.ID)
	}
	for _, taskID := range milestone.Tasks {
		task, err := s.readAnySharedTask(ctx, taskID)
		if err != nil {
			return fmt.Errorf("milestone task %q cannot be resolved: %w", taskID, err)
		}
		status := task.Status
		if state, found, stateErr := s.Durability.ReadTaskExecutionState(ctx, milestone.ProjectID, taskID); stateErr != nil {
			return stateErr
		} else if found && state.Status != model.TaskExecutionDone {
			status = string(state.Status)
		}
		if status != model.TaskAuthoringDone {
			return fmt.Errorf("milestone %q cannot complete: Task %q has status %q", milestone.ID, taskID, status)
		}
	}
	return nil
}

func (s *Service) milestoneView(ctx context.Context, milestone model.Milestone) (MilestoneView, error) {
	tasks := make([]MilestoneTaskProjection, 0, len(milestone.Tasks))
	for _, taskID := range milestone.Tasks {
		task, err := s.readAnySharedTask(ctx, taskID)
		if err != nil {
			return MilestoneView{}, fmt.Errorf("milestone task %q cannot be resolved: %w", taskID, err)
		}
		status := task.Status
		if state, found, stateErr := s.Durability.ReadTaskExecutionState(ctx, milestone.ProjectID, taskID); stateErr != nil {
			return MilestoneView{}, stateErr
		} else if found && state.Status != model.TaskExecutionDone {
			status = string(state.Status)
		}
		tasks = append(tasks, MilestoneTaskProjection{
			Key:      task.ID,
			Title:    task.Title,
			Status:   status,
			Priority: task.Priority,
			Archived: task.Status == model.TaskAuthoringArchived,
		})
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		rank := func(priority string) int {
			value, err := model.TaskPriorityRank(priority)
			if err != nil {
				return len(model.TaskPriorities()) + 1
			}
			return value
		}
		left, right := rank(tasks[i].Priority), rank(tasks[j].Priority)
		if left != right {
			return left < right
		}
		return tasks[i].Key < tasks[j].Key
	})
	report, err := renderMilestoneReport(milestone, tasks)
	if err != nil {
		return MilestoneView{}, err
	}
	return MilestoneView{
		Milestone: milestone,
		Tasks:     tasks,
		Report:    report,
	}, nil
}

func renderMilestoneReport(milestone model.Milestone, tasks []MilestoneTaskProjection) (string, error) {
	symbol, err := model.MilestoneStatusSymbol(milestone.Status)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s [%s]\n", symbol, milestone.Title, milestone.Status)
	if milestone.Summary != "" {
		fmt.Fprintf(&b, "%s\n", milestone.Summary)
	}
	b.WriteString("Ungrouped tasks:\n")
	for index, task := range tasks {
		taskSymbol := "?"
		switch task.Status {
		case model.TaskAuthoringPlanned:
			taskSymbol = "○"
		case model.TaskAuthoringReady:
			taskSymbol = "▶"
		case model.TaskAuthoringDone:
			taskSymbol = "✓"
		case model.TaskAuthoringArchived:
			taskSymbol = "◆"
		case model.TaskExecutionFailed:
			taskSymbol = "✗"
		}
		priority := "P-"
		if task.Priority != "" {
			priority = task.Priority
		}
		fmt.Fprintf(&b, "%d. %s [%s] %s — %s (%s)\n", index+1, taskSymbol, priority, task.Key, task.Title, task.Status)
	}
	return b.String(), nil
}
