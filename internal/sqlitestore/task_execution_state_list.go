package sqlitestore

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// ListTaskExecutionStates returns validated execution authorities for one
// project. It is used only to resolve a session-bound Agent's current Task.
func (d *Databases) ListTaskExecutionStates(ctx context.Context, projectID string) ([]model.TaskExecutionState, error) {
	if d == nil || d.Shared == nil {
		return nil, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT task_id,project_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at FROM shared_task_execution_states WHERE project_id=? ORDER BY task_id`, projectID)
	if err != nil {
		return nil, err
	}
	states := make([]model.TaskExecutionState, 0, len(rows.Rows))
	for _, row := range rows.Rows {
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
