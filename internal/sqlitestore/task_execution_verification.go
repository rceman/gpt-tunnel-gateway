package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (d *Databases) ReadLatestTaskExecutionVerification(ctx context.Context, projectID, taskID string) (model.TaskExecutionVerification, bool, error) {
	if d == nil || d.Local == nil {
		return model.TaskExecutionVerification{}, false, fmt.Errorf("local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT receipt_json FROM local_task_execution_verifications WHERE project_id=? AND task_id=? ORDER BY attempt_revision DESC LIMIT 1`, projectID, taskID)
	if err != nil {
		return model.TaskExecutionVerification{}, false, err
	}
	if len(rows.Rows) == 0 {
		return model.TaskExecutionVerification{}, false, nil
	}
	if len(rows.Rows[0]) != 1 {
		return model.TaskExecutionVerification{}, false, fmt.Errorf("invalid Task verification row")
	}
	text, ok := rows.Rows[0][0].(string)
	if !ok {
		return model.TaskExecutionVerification{}, false, fmt.Errorf("invalid Task verification receipt encoding")
	}
	receipt, err := decodeTaskExecutionVerification(text)
	if err != nil {
		return model.TaskExecutionVerification{}, false, err
	}
	if receipt.ProjectID != projectID || receipt.TaskID != taskID {
		return model.TaskExecutionVerification{}, false, fmt.Errorf("Task verification receipt identity mismatch")
	}
	return receipt, true, nil
}

func (d *Databases) FinishTaskExecutionVerification(ctx context.Context, nextState model.TaskExecutionState, expectedRevision int, receipt model.TaskExecutionVerification) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	if err := model.ValidateTaskExecutionVerification(receipt); err != nil {
		return err
	}
	if err := model.ValidateTaskExecutionState(nextState); err != nil {
		return err
	}
	if expectedRevision != receipt.AttemptRevision || nextState.ExecutionRevision != receipt.AttemptRevision+1 {
		return fmt.Errorf("Task verification finish is not a single CAS step")
	}
	wantStatus := model.TaskExecutionReadyForVerification
	if receipt.Outcome == model.TaskExecutionVerificationSucceeded {
		wantStatus = model.TaskExecutionVerified
	}
	if nextState.Status != wantStatus {
		return fmt.Errorf("Task verification finish status does not match outcome")
	}
	if receipt.ProjectID != nextState.ProjectID || receipt.TaskID != nextState.TaskID || receipt.TaskRevision != nextState.TaskRevision || receipt.TaskRevisionSHA256 != nextState.TaskRevisionSHA256 || receipt.BaseHead != nextState.BaseHead || receipt.CandidateHead != nextState.Head || receipt.Branch != nextState.Branch {
		return fmt.Errorf("Task verification receipt does not match execution state")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	receiptJSON := string(raw)
	if existing, found, readErr := d.taskExecutionVerificationReceipt(ctx, receipt.ProjectID, receipt.TaskID, receipt.OperationID, receipt.AttemptRevision); readErr != nil {
		return readErr
	} else if found {
		return d.reuseTaskExecutionVerification(ctx, nextState, receiptJSON, existing)
	}
	_, err = d.Local.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE local_task_execution_states SET task_revision=?,task_revision_sha256=?,status=?,stage=?,worktree=?,base_head_sha=?,head_sha=?,branch=?,agent=?,execution_revision=?,updated_at=? WHERE project_id=? AND task_id=? AND task_revision=? AND task_revision_sha256=? AND status=? AND stage=? AND worktree=? AND base_head_sha=? AND head_sha=? AND branch=? AND agent=? AND execution_revision=?`, Args: []any{nextState.TaskRevision, nextState.TaskRevisionSHA256, nextState.Status, nextState.Stage, nextState.Worktree, nextState.BaseHead, nextState.Head, nextState.Branch, nextState.Agent, nextState.ExecutionRevision, nextState.UpdatedAt.UTC().Format(time.RFC3339Nano), nextState.ProjectID, nextState.TaskID, nextState.TaskRevision, nextState.TaskRevisionSHA256, model.TaskExecutionVerifying, nextState.Stage, nextState.Worktree, nextState.BaseHead, nextState.Head, nextState.Branch, nextState.Agent, expectedRevision}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO local_task_execution_verifications(project_id,task_id,operation_id,attempt_revision,outcome,receipt_json,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{receipt.ProjectID, receipt.TaskID, receipt.OperationID, receipt.AttemptRevision, receipt.Outcome, receiptJSON, receipt.CompletedAt.UTC().Format(time.RFC3339Nano)}, RequireRowsAffected: 1},
	})
	if err != nil {
		if existing, found, readErr := d.taskExecutionVerificationReceipt(ctx, receipt.ProjectID, receipt.TaskID, receipt.OperationID, receipt.AttemptRevision); readErr == nil && found {
			return d.reuseTaskExecutionVerification(ctx, nextState, receiptJSON, existing)
		}
		return err
	}
	return nil
}

func (d *Databases) taskExecutionVerificationReceipt(ctx context.Context, projectID, taskID, operationID string, attemptRevision int) (string, bool, error) {
	rows, err := d.Local.Query(ctx, `SELECT receipt_json FROM local_task_execution_verifications WHERE project_id=? AND task_id=? AND operation_id=? AND attempt_revision=?`, projectID, taskID, operationID, attemptRevision)
	if err != nil {
		return "", false, err
	}
	if len(rows.Rows) == 0 {
		return "", false, nil
	}
	if len(rows.Rows[0]) != 1 {
		return "", false, fmt.Errorf("invalid Task verification row")
	}
	text, ok := rows.Rows[0][0].(string)
	if !ok {
		return "", false, fmt.Errorf("invalid Task verification receipt encoding")
	}
	return text, true, nil
}

func (d *Databases) reuseTaskExecutionVerification(ctx context.Context, nextState model.TaskExecutionState, receiptJSON, existing string) error {
	if existing != receiptJSON {
		return fmt.Errorf("conflicting Task verification receipt")
	}
	state, found, err := d.ReadTaskExecutionState(ctx, nextState.ProjectID, nextState.TaskID)
	if err != nil {
		return err
	}
	if !found || state.Status != nextState.Status || state.Stage != nextState.Stage || state.Worktree != nextState.Worktree || state.TaskRevision != nextState.TaskRevision || state.TaskRevisionSHA256 != nextState.TaskRevisionSHA256 || state.BaseHead != nextState.BaseHead || state.Head != nextState.Head || state.Branch != nextState.Branch || state.Agent != nextState.Agent || state.ExecutionRevision != nextState.ExecutionRevision {
		return fmt.Errorf("Task verification finish does not match durable state")
	}
	return nil
}

func decodeTaskExecutionVerification(text string) (model.TaskExecutionVerification, error) {
	var receipt model.TaskExecutionVerification
	if err := json.Unmarshal([]byte(text), &receipt); err != nil {
		return model.TaskExecutionVerification{}, fmt.Errorf("invalid Task verification receipt payload")
	}
	if err := model.ValidateTaskExecutionVerification(receipt); err != nil {
		return model.TaskExecutionVerification{}, err
	}
	return receipt, nil
}
