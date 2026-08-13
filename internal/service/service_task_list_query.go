package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

type taskListCandidate struct {
	task  model.Task
	state model.TaskState
}

type taskListCursor struct {
	CreatedAt time.Time `json:"created_at"`
	TaskID    string    `json:"task_id"`
}

func (s *Service) TaskList(ctx context.Context, project string) ([]TaskRecord, error) {
	result, err := s.taskListQuery(ctx, TaskListInput{ProjectID: project}, true)
	if err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

func (s *Service) TaskListQuery(ctx context.Context, in TaskListInput) (TaskListResult, error) {
	return s.taskListQuery(ctx, in, false)
}

func (s *Service) taskListQuery(ctx context.Context, in TaskListInput, unbounded bool) (TaskListResult, error) {
	if strings.TrimSpace(in.ProjectID) == "" {
		return TaskListResult{}, fmt.Errorf("project_id is required")
	}
	status := strings.TrimSpace(in.Status)
	if status != "" && !validTaskListStatus(status) {
		return TaskListResult{}, fmt.Errorf("invalid task status %q", status)
	}
	query := strings.TrimSpace(in.Query)
	if len(query) > 256 {
		return TaskListResult{}, fmt.Errorf("task query exceeds 256 characters")
	}
	limit := 0
	if !unbounded {
		var err error
		limit, err = s.taskListLimit(in.Limit)
		if err != nil {
			return TaskListResult{}, err
		}
	}
	records, err := s.entityRegistry(in.ProjectID).ListRecords(ctx, entity.Query{Family: entity.TaskFamily})
	if err != nil {
		return TaskListResult{}, err
	}
	candidates := make([]taskListCandidate, 0, len(records))
	for _, record := range records {
		var task model.Task
		if err := decodeStrict(record.Bytes, &task); err != nil {
			return TaskListResult{}, err
		}
		state, err := s.taskState(ctx, task)
		if err != nil {
			return TaskListResult{}, err
		}
		if status != "" && state.Status != status {
			continue
		}
		if query != "" && !taskMatchesQuery(task, state, query) {
			continue
		}
		candidates = append(candidates, taskListCandidate{
			task:  task,
			state: state,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].task.CreatedAt.Equal(candidates[j].task.CreatedAt) {
			return candidates[i].task.ID > candidates[j].task.ID
		}
		return candidates[i].task.CreatedAt.After(candidates[j].task.CreatedAt)
	})
	if !unbounded && in.Cursor != "" {
		keys := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			keys = append(keys, taskCursorKey(candidate.task))
		}
		after, err := resolveTaskListCursor(in.Cursor, taskListCursorKind(in), keys)
		if err != nil {
			return TaskListResult{}, err
		}
		filtered := candidates[:0]
		for _, candidate := range candidates {
			if taskCursorKey(candidate.task) < after {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	hasMore := false
	if !unbounded && len(candidates) > limit {
		hasMore = true
		candidates = candidates[:limit]
	}
	if len(candidates) == 0 {
		return TaskListResult{
			Tasks:   []TaskRecord{},
			HasMore: false,
		}, nil
	}
	items := make([]TaskRecord, 0, len(candidates))
	for _, candidate := range candidates {
		var currentRevision *model.TaskRevision
		if model.ValidateCanonicalTaskID(candidate.task.ID) == nil {
			revision, revisionErr := s.currentTaskRevision(ctx, candidate.task)
			if revisionErr != nil {
				return TaskListResult{}, revisionErr
			}
			currentRevision = &revision
		}
		items = append(items, TaskRecord{
			Task:            candidate.task,
			State:           candidate.state,
			CurrentRevision: currentRevision,
		})
	}
	result := TaskListResult{
		Tasks:   items,
		HasMore: hasMore,
	}
	if hasMore {
		result.NextCursor = pagination.Encode(taskListCursorKind(in), taskCursorKey(items[len(items)-1].Task))
	}
	return result, nil
}

func (s *Service) taskListLimit(requested int) (int, error) {
	max := s.Config.MaxListItems
	if max < 1 || max > MaxTaskListLimit {
		max = MaxTaskListLimit
	}
	if requested == 0 {
		if DefaultTaskListLimit < max {
			return DefaultTaskListLimit, nil
		}
		return max, nil
	}
	if requested < 1 || requested > max {
		return 0, fmt.Errorf("task list limit must be between 1 and %d", max)
	}
	return requested, nil
}

func validTaskListStatus(status string) bool {
	switch status {
	case "created", "ready", "dispatched", "cancelled", "superseded", "completed", "merge_ready", "deferred", "merged":
		return true
	default:
		return false
	}
}

func taskMatchesQuery(task model.Task, state model.TaskState, query string) bool {
	slug := strings.TrimPrefix(task.Branch, "task/"+task.ID+"-")
	text := strings.ToLower(strings.Join([]string{task.ID, slug, task.Branch, task.Title, task.Objective, task.CreatedBy, task.OperationClass, task.Status, state.Status, task.Supersedes}, "\n"))
	for _, criterion := range append(append([]string{}, task.AcceptanceCriteria...), task.Constraints...) {
		text += "\n" + strings.ToLower(criterion)
	}
	return strings.Contains(text, strings.ToLower(query))
}

func taskAfterCursor(task model.Task, cursor taskListCursor) bool {
	return task.CreatedAt.Before(cursor.CreatedAt) || (task.CreatedAt.Equal(cursor.CreatedAt) && task.ID < cursor.TaskID)
}

func taskCursorKey(task model.Task) string {
	return task.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + task.ID
}

func taskListCursorKind(in TaskListInput) string {
	return "task_list:" + in.ProjectID + "|" + strings.TrimSpace(in.Status) + "|" + strings.TrimSpace(in.Query)
}

func resolveTaskListCursor(value, kind string, keys []string) (string, error) {
	if len(value) <= pagination.CompactCursorLength {
		return pagination.Resolve(value, kind, keys)
	}
	legacy, err := decodeTaskListCursor(value)
	if err != nil {
		return "", err
	}
	return taskCursorKey(model.Task{ID: legacy.TaskID, CreatedAt: legacy.CreatedAt}), nil
}

func encodeTaskListCursor(cursor taskListCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeTaskListCursor(value string) (taskListCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return taskListCursor{}, fmt.Errorf("invalid task list cursor")
	}
	var cursor taskListCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.TaskID == "" || cursor.CreatedAt.IsZero() {
		return taskListCursor{}, fmt.Errorf("invalid task list cursor")
	}
	return cursor, nil
}
