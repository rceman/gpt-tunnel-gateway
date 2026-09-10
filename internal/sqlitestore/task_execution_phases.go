package sqlitestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskExecutionPhase struct {
	ID                 int64
	TaskID             string
	ProjectID          string
	ExecutionRevision  int
	Stage              string
	Status             string
	Head               string
	Branch             string
	TaskRevisionSHA256 string
	EventKind          string
	Decision           string
	Comment            string
	CreatedAt          time.Time
}

func (d *Databases) AppendTaskExecutionPhase(ctx context.Context, phase TaskExecutionPhase) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if err := validateTaskExecutionPhase(phase); err != nil {
		return err
	}
	if phase.CreatedAt.IsZero() {
		return fmt.Errorf("incomplete Task execution phase")
	}
	_, err := d.Shared.Batch(ctx, []upstream.Statement{{SQL: `INSERT INTO shared_task_execution_phases(task_id,project_id,execution_revision,stage,status,head_sha,branch,task_revision_sha256,event_kind,decision,comment,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Args: []any{phase.TaskID, phase.ProjectID, phase.ExecutionRevision, phase.Stage, phase.Status, phase.Head, phase.Branch, phase.TaskRevisionSHA256, phase.EventKind, phase.Decision, phase.Comment, phase.CreatedAt.UTC().Format(time.RFC3339Nano)}}})
	return err
}

func (d *Databases) ReadLatestTaskExecutionPhase(ctx context.Context, projectID, taskID, stage string) (TaskExecutionPhase, bool, error) {
	if d == nil || d.Shared == nil {
		return TaskExecutionPhase{}, false, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT id,task_id,project_id,execution_revision,stage,status,head_sha,branch,task_revision_sha256,event_kind,COALESCE(decision,''),COALESCE(comment,''),created_at FROM shared_task_execution_phases WHERE project_id=? AND task_id=? AND stage=? ORDER BY id DESC LIMIT 1`, projectID, taskID, stage)
	if err != nil {
		return TaskExecutionPhase{}, false, err
	}
	if len(rows.Rows) == 0 {
		return TaskExecutionPhase{}, false, nil
	}
	if len(rows.Rows[0]) != 13 {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase row")
	}
	r := rows.Rows[0]
	values := make([]string, 0, 11)
	for _, index := range []int{1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
		value, ok := r[index].(string)
		if !ok {
			return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase value types")
		}
		values = append(values, value)
	}
	id, ok := r[0].(int64)
	if !ok {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase id")
	}
	revision, ok := r[3].(int64)
	if !ok {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase revision")
	}
	created, err := time.Parse(time.RFC3339Nano, values[10])
	if err != nil {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase timestamp")
	}
	phase := TaskExecutionPhase{
		ID:                 id,
		TaskID:             values[0],
		ProjectID:          values[1],
		ExecutionRevision:  int(revision),
		Stage:              values[2],
		Status:             values[3],
		Head:               values[4],
		Branch:             values[5],
		TaskRevisionSHA256: values[6],
		EventKind:          values[7],
		Decision:           values[8],
		Comment:            values[9],
		CreatedAt:          created,
	}
	if err := validateTaskExecutionPhase(phase); err != nil {
		return TaskExecutionPhase{}, false, err
	}
	return phase, true, nil
}

func (d *Databases) ReadLatestAcceptedTaskExecutionPhase(ctx context.Context, projectID, taskID, stage string) (TaskExecutionPhase, bool, error) {
	return d.readTaskExecutionPhaseWhere(ctx, projectID, taskID, stage, `decision='accept'`)
}

func (d *Databases) readTaskExecutionPhaseWhere(ctx context.Context, projectID, taskID, stage, predicate string) (TaskExecutionPhase, bool, error) {
	rows, err := d.Shared.Query(ctx, `SELECT id,task_id,project_id,execution_revision,stage,status,head_sha,branch,task_revision_sha256,event_kind,COALESCE(decision,''),COALESCE(comment,''),created_at FROM shared_task_execution_phases WHERE project_id=? AND task_id=? AND stage=? AND `+predicate+` ORDER BY id DESC LIMIT 1`, projectID, taskID, stage)
	if err != nil {
		return TaskExecutionPhase{}, false, err
	}
	if len(rows.Rows) == 0 {
		return TaskExecutionPhase{}, false, nil
	}
	r := rows.Rows[0]
	if len(r) != 13 {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase row")
	}
	phase := TaskExecutionPhase{}
	if v, ok := r[0].(int64); ok {
		phase.ID = v
	} else {
		return phase, false, fmt.Errorf("invalid Task execution phase id")
	}
	stringFields := []*string{&phase.TaskID, &phase.ProjectID, &phase.Stage, &phase.Status, &phase.Head, &phase.Branch, &phase.TaskRevisionSHA256, &phase.EventKind, &phase.Decision, &phase.Comment}
	for i, p := range []int{1, 2, 4, 5, 6, 7, 8, 9, 10, 11} {
		v, ok := r[p].(string)
		if !ok {
			return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase value types")
		}
		*stringFields[i] = v
	}
	v, ok := r[3].(int64)
	if !ok {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase revision")
	}
	phase.ExecutionRevision = int(v)
	createdValue, ok := r[12].(string)
	if !ok {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase timestamp")
	}
	created, err := time.Parse(time.RFC3339Nano, createdValue)
	if err != nil {
		return TaskExecutionPhase{}, false, fmt.Errorf("invalid Task execution phase timestamp")
	}
	phase.CreatedAt = created
	if err := validateTaskExecutionPhase(phase); err != nil {
		return TaskExecutionPhase{}, false, err
	}
	return phase, true, nil
}

func validateTaskExecutionPhase(phase TaskExecutionPhase) error {
	if model.ValidateProjectIdentifier(phase.ProjectID) != nil || model.ValidateCanonicalTaskID(phase.TaskID) != nil || phase.Stage == "" || phase.Status == "" || phase.ExecutionRevision < 1 || phase.Head == "" || phase.Branch == "" || phase.TaskRevisionSHA256 == "" {
		return fmt.Errorf("incomplete Task execution phase")
	}
	if phase.EventKind != "submission" && phase.EventKind != "review" && phase.EventKind != "rework" {
		return fmt.Errorf("invalid Task execution phase event kind")
	}
	if phase.Decision != "" && phase.Decision != "accept" && phase.Decision != "reject" {
		return fmt.Errorf("invalid Task execution phase decision")
	}
	if phase.Stage != "code" && phase.Stage != "tests" && phase.Stage != "rebase" {
		return fmt.Errorf("invalid Task execution phase stage")
	}
	if len(phase.Head) != 40 || model.ValidateCommitSHA(phase.Head) != nil || model.ValidateSHA256(phase.TaskRevisionSHA256) != nil || model.ValidateBranch(phase.Branch) != nil || !strings.HasPrefix(phase.Branch, "task/"+phase.TaskID+"-") {
		return fmt.Errorf("invalid Task execution phase authority")
	}
	return nil
}

func (d *Databases) UpdateTaskExecutionState(ctx context.Context, state model.TaskExecutionState, expectedRevision int) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return err
	}
	_, err := d.Shared.Batch(ctx, []upstream.Statement{{SQL: `UPDATE shared_task_execution_states SET status=?,stage=?,head_sha=?,execution_revision=?,updated_at=? WHERE project_id=? AND task_id=? AND execution_revision=?`, Args: []any{state.Status, state.Stage, state.Head, state.ExecutionRevision, state.UpdatedAt.UTC().Format(time.RFC3339Nano), state.ProjectID, state.TaskID, expectedRevision}, RequireRowsAffected: 1}})
	return err
}

func (d *Databases) TransitionTaskExecutionState(ctx context.Context, state model.TaskExecutionState, expectedRevision int, phase TaskExecutionPhase) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		return err
	}
	if phase.TaskID != state.TaskID || phase.ProjectID != state.ProjectID || phase.ExecutionRevision != state.ExecutionRevision || phase.Head != state.Head || phase.Branch != state.Branch || phase.TaskRevisionSHA256 != state.TaskRevisionSHA256 {
		return fmt.Errorf("Task execution phase does not match state")
	}
	if err := validateTaskExecutionPhase(phase); err != nil {
		return err
	}
	_, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_task_execution_states SET status=?,stage=?,head_sha=?,execution_revision=?,updated_at=? WHERE project_id=? AND task_id=? AND execution_revision=?`, Args: []any{state.Status, state.Stage, state.Head, state.ExecutionRevision, state.UpdatedAt.UTC().Format(time.RFC3339Nano), state.ProjectID, state.TaskID, expectedRevision}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_task_execution_phases(task_id,project_id,execution_revision,stage,status,head_sha,branch,task_revision_sha256,event_kind,decision,comment,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Args: []any{phase.TaskID, phase.ProjectID, phase.ExecutionRevision, phase.Stage, phase.Status, phase.Head, phase.Branch, phase.TaskRevisionSHA256, phase.EventKind, phase.Decision, phase.Comment, phase.CreatedAt.UTC().Format(time.RFC3339Nano)}, RequireRowsAffected: 1},
	})
	return err
}
