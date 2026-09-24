package sqlitestore

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const TaskExecutionStatePageMaxRows = 100

type TaskExecutionStatePageCursor struct {
	UpdatedAt string
	TaskID    string
	Worktree  string
}

type TaskExecutionStatePage struct {
	States     []model.TaskExecutionState
	HasMore    bool
	NextCursor TaskExecutionStatePageCursor
}

func (d *Databases) ListTaskExecutionStatesPage(ctx context.Context, projectID, query string, after TaskExecutionStatePageCursor, limit int) (TaskExecutionStatePage, error) {
	if d == nil || d.Local == nil {
		return TaskExecutionStatePage{}, fmt.Errorf("local store is unavailable")
	}
	if limit < 1 || limit > TaskExecutionStatePageMaxRows {
		return TaskExecutionStatePage{}, fmt.Errorf("invalid Task execution state page limit")
	}
	if after.TaskID != "" {
		rows, err := d.Local.Query(ctx, `SELECT COUNT(*) FROM local_task_execution_states WHERE project_id=? AND task_id=? AND worktree=? AND updated_at=? AND status NOT IN (?,?)`, projectID, after.TaskID, after.Worktree, after.UpdatedAt, model.TaskExecutionDone, model.TaskExecutionFailed)
		if err != nil {
			return TaskExecutionStatePage{}, err
		}
		if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 || rows.Rows[0][0] != int64(1) {
			return TaskExecutionStatePage{}, fmt.Errorf("continuation cursor is no longer valid")
		}
	}
	where := `project_id=? AND status NOT IN (?,?)`
	args := []any{projectID, model.TaskExecutionDone, model.TaskExecutionFailed}
	if query != "" {
		where += ` AND (instr(worktree,?) > 0 OR instr(task_id,?) > 0)`
		args = append(args, query, query)
	}
	if after.TaskID != "" {
		where += ` AND (updated_at < ? OR (updated_at = ? AND task_id < ?))`
		args = append(args, after.UpdatedAt, after.UpdatedAt, after.TaskID)
	}
	args = append(args, limit+1)
	rows, err := d.Local.Query(ctx, `SELECT task_id,project_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at FROM local_task_execution_states WHERE `+where+` ORDER BY updated_at DESC,task_id DESC LIMIT ?`, args...)
	if err != nil {
		return TaskExecutionStatePage{}, err
	}
	states, err := decodeTaskExecutionStateRows(rows.Rows)
	if err != nil {
		return TaskExecutionStatePage{}, err
	}
	page := TaskExecutionStatePage{States: states}
	if len(states) > limit {
		page.HasMore = true
		page.States = states[:limit]
		last := page.States[len(page.States)-1]
		page.NextCursor = TaskExecutionStatePageCursor{
			UpdatedAt: last.UpdatedAt.UTC().Format(time.RFC3339Nano),
			TaskID:    last.TaskID,
			Worktree:  last.Worktree,
		}
	}
	return page, nil
}

func decodeTaskExecutionStateRows(rows [][]any) ([]model.TaskExecutionState, error) {
	states := make([]model.TaskExecutionState, 0, len(rows))
	for _, row := range rows {
		if len(row) != 13 {
			return nil, fmt.Errorf("invalid Task execution state row")
		}
		stringAt := func(index int) (string, bool) { value, ok := row[index].(string); return value, ok }
		taskID, ok0 := stringAt(0)
		project, ok1 := stringAt(1)
		taskRevision, ok2 := row[2].(int64)
		taskHash, ok3 := stringAt(3)
		status, ok4 := stringAt(4)
		stage, ok5 := stringAt(5)
		worktree, ok6 := stringAt(6)
		base, ok7 := stringAt(7)
		head, ok8 := stringAt(8)
		branch, ok9 := stringAt(9)
		agent, ok10 := stringAt(10)
		revision, ok11 := row[11].(int64)
		updated, ok12 := stringAt(12)
		if !(ok0 && ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8 && ok9 && ok10 && ok11 && ok12) {
			return nil, fmt.Errorf("invalid Task execution state value types")
		}
		updatedAt, parseErr := time.Parse(time.RFC3339Nano, updated)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid Task execution timestamp")
		}
		state := model.TaskExecutionState{TaskID: taskID, ProjectID: project, TaskRevision: int(taskRevision), TaskRevisionSHA256: taskHash, Status: status, Stage: stage, Worktree: worktree, BaseHead: base, Head: head, Branch: branch, Agent: agent, ExecutionRevision: int(revision), UpdatedAt: updatedAt}
		if err := model.ValidateTaskExecutionState(state); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
