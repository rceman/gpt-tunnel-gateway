package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const indexPageSize = 10

type GitLogInput struct {
	ProjectID string `json:"project_id"`
	Revision  string `json:"revision"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type GitLogRow struct {
	ShortSHA string `json:"short_sha"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
}

type GitLogPage struct {
	Commits []GitLogRow `json:"commits"`
	Cursor  string      `json:"cursor"`
}

type TaskListInput struct {
	ProjectID string `json:"project_id"`
	Status    string `json:"status,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type TaskIndexRow struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Status          string  `json:"status"`
	UpdatedAt       string  `json:"updated_at"`
	LatestRunID     *string `json:"latest_run_id"`
	LatestRunStatus *string `json:"latest_run_status"`
}

type TaskListPage struct {
	Tasks  []TaskIndexRow `json:"tasks"`
	Cursor string         `json:"cursor"`
}

type TaskNextResult struct {
	Task *TaskIndexRow `json:"task"`
}

func validatePageLimit(limit int) (int, error) {
	if limit == 0 {
		return indexPageSize, nil
	}
	if limit < 1 || limit > indexPageSize {
		return 0, fmt.Errorf("limit must be between 1 and %d", indexPageSize)
	}
	return limit, nil
}

type indexCursor = boundedIndexCursor

func encodeIndexCursor(c indexCursor) (string, error)   { return encodeBoundedIndexCursor(c) }
func decodeIndexCursor(raw string) (indexCursor, error) { return decodeBoundedIndexCursor(raw) }

func validCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (s *Service) GitLogPage(ctx context.Context, in GitLogInput) (GitLogPage, error) {
	limit, err := validatePageLimit(in.Limit)
	if err != nil {
		return GitLogPage{}, err
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return GitLogPage{}, err
	}
	c := indexCursor{Version: 1, Kind: "git_log", ProjectID: in.ProjectID, Query: in.Revision, Limit: limit}
	offset := 0
	if in.Cursor != "" {
		c, err = decodeIndexCursor(in.Cursor)
		if err != nil || c.Kind != "git_log" || c.ProjectID != in.ProjectID || c.Query != in.Revision || c.Limit != limit || !validCommitSHA(c.Root) {
			return GitLogPage{}, fmt.Errorf("invalid git_log cursor")
		}
		offset = c.Offset
	} else {
		if in.Revision == "" {
			return GitLogPage{}, fmt.Errorf("revision is required")
		}
		c.Root, err = s.Git.ResolveRevision(ctx, project, in.Revision)
		if err != nil {
			return GitLogPage{}, err
		}
	}
	commits, err := s.Git.LogAt(ctx, project, c.Root, offset, limit+1)
	if err != nil {
		return GitLogPage{}, err
	}
	page := GitLogPage{Commits: make([]GitLogRow, 0, projectionMinInt(len(commits), limit)), Cursor: ""}
	for _, commit := range commits[:projectionMinInt(len(commits), limit)] {
		if !validCommitSHA(commit.SHA) {
			return GitLogPage{}, fmt.Errorf("git log returned invalid commit SHA")
		}
		page.Commits = append(page.Commits, GitLogRow{ShortSHA: commit.SHA[:10], Commit: commit.Subject, Date: commit.CommitDate})
	}
	if len(commits) > limit {
		c.Offset = offset + limit
		page.Cursor, err = encodeIndexCursor(c)
		if err != nil {
			return GitLogPage{}, err
		}
	}
	return page, nil
}

func projectionMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Service) TaskListPage(ctx context.Context, in TaskListInput) (TaskListPage, error) {
	limit, err := validatePageLimit(in.Limit)
	if err != nil {
		return TaskListPage{}, err
	}
	if _, err := s.projectConfig(in.ProjectID); err != nil {
		return TaskListPage{}, err
	}
	c := indexCursor{Version: 1, Kind: "task_list", ProjectID: in.ProjectID, Filter: in.Status, Limit: limit}
	offset := 0
	if in.Cursor != "" {
		c, err = decodeIndexCursor(in.Cursor)
		if err != nil || c.Kind != "task_list" || c.ProjectID != in.ProjectID || c.Filter != in.Status || c.Limit != limit || !validCommitSHA(c.Root) {
			return TaskListPage{}, fmt.Errorf("invalid task_list cursor")
		}
		offset = c.Offset
	} else {
		c.Root, err = s.Hub.RemoteRevision(ctx)
		if err != nil {
			return TaskListPage{}, err
		}
		if !validCommitSHA(c.Root) {
			return TaskListPage{}, fmt.Errorf("invalid task_list root revision")
		}
	}
	rows, err := s.taskIndexRowsAt(ctx, in.ProjectID, c.Root)
	if err != nil {
		return TaskListPage{}, err
	}
	if in.Status != "" {
		filtered := rows[:0]
		for _, row := range rows {
			if row.Status == in.Status {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt == rows[j].UpdatedAt {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})
	if offset > len(rows) {
		return TaskListPage{}, fmt.Errorf("invalid task_list cursor offset")
	}
	end := projectionMinInt(offset+limit, len(rows))
	page := TaskListPage{Tasks: rows[offset:end], Cursor: ""}
	if end < len(rows) {
		c.Offset = end
		page.Cursor, err = encodeIndexCursor(c)
		if err != nil {
			return TaskListPage{}, err
		}
	}
	return page, nil
}

func (s *Service) taskIndexRowsAt(ctx context.Context, projectID, root string) ([]TaskIndexRow, error) {
	taskPaths, err := s.Hub.ListAt(ctx, root, s.projectPrefix(projectID)+"/tasks", ".json")
	if err != nil {
		return nil, err
	}
	runPaths, err := s.Hub.ListAt(ctx, root, s.projectPrefix(projectID)+"/runs", "/run.json")
	if err != nil {
		return nil, err
	}
	latestRuns := map[string]model.Run{}
	for _, path := range runPaths {
		data, err := s.Hub.ReadFileAtRevision(ctx, root, path)
		if err != nil {
			return nil, err
		}
		run, _, err := model.DecodeRunRecord(data)
		if err != nil {
			return nil, err
		}
		current, ok := latestRuns[run.TaskID]
		if !ok || run.CreatedAt.After(current.CreatedAt) || (run.CreatedAt.Equal(current.CreatedAt) && run.ID > current.ID) {
			latestRuns[run.TaskID] = run
		}
	}
	rows := make([]TaskIndexRow, 0, len(taskPaths))
	for _, path := range taskPaths {
		if strings.HasSuffix(path, ".state.json") || strings.HasSuffix(path, ".run-counter.json") || strings.Contains(path, "/revisions/") {
			continue
		}
		var task model.Task
		if err := s.Hub.ReadJSONAtRevision(ctx, root, path, &task); err != nil {
			return nil, err
		}
		var state model.TaskState
		if err := s.Hub.ReadJSONAtRevision(ctx, root, s.taskStatePath(projectID, task.ID), &state); err != nil {
			if !IsNotFound(err) {
				return nil, err
			}
			state = model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: task.CreatedAt}
		}
		if err := model.ValidateTaskState(state, task); err != nil {
			return nil, err
		}
		row := TaskIndexRow{ID: task.ID, Title: task.Title, Status: state.Status, UpdatedAt: state.UpdatedAt.UTC().Format(time.RFC3339Nano)}
		if run, ok := latestRuns[task.ID]; ok {
			runID, runStatus := run.ID, run.Status
			row.LatestRunID = &runID
			row.LatestRunStatus = &runStatus
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt == rows[j].UpdatedAt {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})
	return rows, nil
}

// TaskNext is the sole bounded selector for the next task eligible to begin.
// Eligibility follows the dispatch contract: only created and ready task
// states can begin an operational run. Pages are traversed in their pinned,
// deterministic order so an ineligible index row cannot hide a later choice.
func (s *Service) TaskNext(ctx context.Context, projectID string) (TaskNextResult, error) {
	var cursor string
	for {
		page, err := s.TaskListPage(ctx, TaskListInput{ProjectID: projectID, Limit: indexPageSize, Cursor: cursor})
		if err != nil {
			return TaskNextResult{}, err
		}
		for i := range page.Tasks {
			if page.Tasks[i].Status == "created" || page.Tasks[i].Status == "ready" {
				return TaskNextResult{Task: &page.Tasks[i]}, nil
			}
		}
		if page.Cursor == "" {
			return TaskNextResult{}, nil
		}
		cursor = page.Cursor
	}
}
