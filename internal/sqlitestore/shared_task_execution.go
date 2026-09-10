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
	rows, err := d.Shared.Query(ctx, `SELECT task_id,project_id,status,stage,worktree,head_sha,agent,execution_revision,updated_at FROM shared_task_execution_states WHERE project_id=? AND task_id=?`, projectID, taskID)
	if err != nil {
		return model.TaskExecutionState{}, false, err
	}
	if len(rows.Rows) == 0 {
		return model.TaskExecutionState{}, false, nil
	}
	if len(rows.Rows[0]) != 9 {
		return model.TaskExecutionState{}, false, fmt.Errorf("invalid Task execution state row")
	}
	r := rows.Rows[0]
	state := model.TaskExecutionState{TaskID: r[0].(string), ProjectID: r[1].(string), Status: r[2].(string), Stage: r[3].(string), Worktree: r[4].(string), Head: r[5].(string), Agent: r[6].(string), ExecutionRevision: int(r[7].(int64))}
	state.UpdatedAt, err = time.Parse(time.RFC3339Nano, r[8].(string))
	if err != nil {
		return model.TaskExecutionState{}, false, fmt.Errorf("invalid Task execution timestamp")
	}
	return state, true, nil
}

func (d *Databases) CreateTaskExecutionState(ctx context.Context, state model.TaskExecutionState) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	_, err := d.Shared.Batch(ctx, []upstream.Statement{{SQL: `INSERT INTO shared_task_execution_states(task_id,project_id,status,stage,worktree,head_sha,agent,execution_revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, Args: []any{state.TaskID, state.ProjectID, state.Status, state.Stage, state.Worktree, state.Head, state.Agent, state.ExecutionRevision, state.UpdatedAt.UTC().Format(time.RFC3339Nano)}}})
	return err
}
