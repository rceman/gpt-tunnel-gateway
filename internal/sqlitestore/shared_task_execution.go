package sqlitestore

import (
	"context"
	"fmt"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (d *Databases) ReadTaskExecutionState(ctx context.Context, projectID, taskID string) (model.TaskExecutionState, bool, error) {
	if d == nil || d.Shared == nil {
		return model.TaskExecutionState{}, false, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT task_id,project_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at FROM shared_task_execution_states WHERE project_id=? AND task_id=?`, projectID, taskID)
	if err != nil {
		return model.TaskExecutionState{}, false, err
	}
	if len(rows.Rows) == 0 {
		return model.TaskExecutionState{}, false, nil
	}
	if len(rows.Rows[0]) != 13 {
		return model.TaskExecutionState{}, false, fmt.Errorf("invalid Task execution state row")
	}
	r := rows.Rows[0]
	stringAt := func(value any) (string, bool) { v, ok := value.(string); return v, ok }
	taskID, ok0 := stringAt(r[0])
	project, ok1 := stringAt(r[1])
	taskRevision, ok2 := r[2].(int64)
	taskHash, ok3 := stringAt(r[3])
	status, ok4 := stringAt(r[4])
	stage, ok5 := stringAt(r[5])
	worktree, ok6 := stringAt(r[6])
	base, ok7 := stringAt(r[7])
	head, ok8 := stringAt(r[8])
	branch, ok9 := stringAt(r[9])
	agent, ok10 := stringAt(r[10])
	revision, ok11 := r[11].(int64)
	updated, ok12 := stringAt(r[12])
	if !(ok0 && ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8 && ok9 && ok10 && ok11 && ok12) {
		return model.TaskExecutionState{}, false, fmt.Errorf("invalid Task execution state value types")
	}
	state := model.TaskExecutionState{TaskID: taskID, ProjectID: project, TaskRevision: int(taskRevision), TaskRevisionSHA256: taskHash, Status: status, Stage: stage, Worktree: worktree, BaseHead: base, Head: head, Branch: branch, Agent: agent, ExecutionRevision: int(revision)}
	state.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return model.TaskExecutionState{}, false, fmt.Errorf("invalid Task execution timestamp")
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return model.TaskExecutionState{}, false, err
	}
	return state, true, nil
}

func (d *Databases) CreateTaskExecutionState(ctx context.Context, state model.TaskExecutionState) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return err
	}
	_, err := d.Shared.Batch(ctx, []upstream.Statement{{SQL: `INSERT INTO shared_task_execution_states(task_id,project_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, Args: []any{state.TaskID, state.ProjectID, state.TaskRevision, state.TaskRevisionSHA256, state.Status, state.Stage, state.Worktree, state.BaseHead, state.Head, state.Branch, state.Agent, state.ExecutionRevision, state.UpdatedAt.UTC().Format(time.RFC3339Nano)}}})
	return err
}
