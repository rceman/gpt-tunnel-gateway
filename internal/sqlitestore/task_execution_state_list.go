package sqlitestore

import (
	"context"
	"fmt"

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
	return decodeTaskExecutionStateRows(rows.Rows)
}
