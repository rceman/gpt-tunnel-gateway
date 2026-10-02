package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (d *Databases) ReadLatestTaskExecutionVerification(ctx context.Context, projectID, taskID string) (model.TaskExecutionVerification, bool, error) {
	if d == nil || d.Local == nil {
		return model.TaskExecutionVerification{}, false, fmt.Errorf("local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT receipt_json FROM local_task_execution_verifications WHERE project_id=? AND task_id=? AND NOT EXISTS(SELECT 1 FROM local_project_retirements WHERE project_id=local_task_execution_verifications.project_id) ORDER BY attempt_revision DESC LIMIT 1`, projectID, taskID)
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

// ReadLatestTaskExecutionVerificationTolerant reads the latest verification
// like ReadLatestTaskExecutionVerification, but a structurally well-formed
// pre-Procedure-gate receipt that fails the current contract is returned with
// legacy=true instead of erroring. It exists only so history read paths stay
// usable for tasks verified under the pre-execution model; current-era writes
// still go through the strict decode and validation.
func (d *Databases) ReadLatestTaskExecutionVerificationTolerant(ctx context.Context, projectID, taskID string) (model.TaskExecutionVerification, bool, bool, error) {
	receipt, found, err := d.ReadLatestTaskExecutionVerification(ctx, projectID, taskID)
	if err == nil {
		return receipt, found, false, nil
	}
	rows, qerr := d.Local.Query(ctx, `SELECT receipt_json FROM local_task_execution_verifications WHERE project_id=? AND task_id=? AND NOT EXISTS(SELECT 1 FROM local_project_retirements WHERE project_id=local_task_execution_verifications.project_id) ORDER BY attempt_revision DESC LIMIT 1`, projectID, taskID)
	if qerr != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return model.TaskExecutionVerification{}, false, false, err
	}
	text, ok := rows.Rows[0][0].(string)
	if !ok {
		return model.TaskExecutionVerification{}, false, false, err
	}
	legacy, lerr := decodeTaskExecutionVerificationLegacy(text)
	if lerr != nil {
		return model.TaskExecutionVerification{}, false, false, err
	}
	if legacy.ProjectID != projectID || legacy.TaskID != taskID {
		return model.TaskExecutionVerification{}, false, false, err
	}
	return legacy, true, true, nil
}

// decodeTaskExecutionVerificationLegacy validates only the structural shape of
// a persisted verification receipt — present identity fields, well-formed
// digests/heads, sane timing, and executed gates with exit codes — without the
// current Procedure-gate cross-bindings that pre-execution receipts predate.
// DecodeTaskExecutionVerificationLegacy exposes the bounded structural
// decoder to the transition reconciliation so it can validate historical
// receipts the current Procedure-gate contract rejects.
func DecodeTaskExecutionVerificationLegacy(text string) (model.TaskExecutionVerification, error) {
	return decodeTaskExecutionVerificationLegacy(text)
}

func decodeTaskExecutionVerificationLegacy(text string) (model.TaskExecutionVerification, error) {
	var receipt model.TaskExecutionVerification
	if err := json.Unmarshal([]byte(text), &receipt); err != nil {
		return model.TaskExecutionVerification{}, fmt.Errorf("invalid Task verification receipt payload")
	}
	if model.ValidateProjectIdentifier(receipt.ProjectID) != nil || model.ValidateCanonicalTaskID(receipt.TaskID) != nil || model.ValidateObjectIdentifier(receipt.OperationID) != nil {
		return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification identity")
	}
	if model.ValidateSHA256(receipt.TaskRevisionSHA256) != nil || model.ValidateSHA256(receipt.GateProfileSHA256) != nil || model.ValidateCommitSHA(receipt.BaseHead) != nil || model.ValidateCommitSHA(receipt.CandidateHead) != nil || model.ValidateCommitSHA(receipt.CandidateTree) != nil {
		return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification digests")
	}
	if model.ValidateBranch(receipt.Branch) != nil || !strings.HasPrefix(receipt.Branch, "task/"+receipt.TaskID+"-") || receipt.TaskRevision < 1 || receipt.AttemptRevision < 1 || receipt.CodeReviewID < 1 || receipt.TestsReviewID < 0 || receipt.RebaseReviewID < 0 {
		return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification revision authority")
	}
	if receipt.StartedAt.IsZero() || receipt.CompletedAt.IsZero() || !receipt.CompletedAt.After(receipt.StartedAt) {
		return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification timing")
	}
	switch receipt.Outcome {
	case model.TaskExecutionVerificationSucceeded:
		if receipt.Error != "" || len(receipt.Gates) == 0 {
			return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification outcome evidence")
		}
	case model.TaskExecutionVerificationFailed, model.TaskExecutionVerificationInterrupted:
	default:
		return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification outcome")
	}
	seen := map[string]struct{}{}
	for _, gate := range receipt.Gates {
		if _, dup := seen[gate.ID]; dup {
			return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification gates")
		}
		seen[gate.ID] = struct{}{}
		if gate.ID == "" || (gate.Execution != "" && gate.Execution != "executed") || (gate.ReceiptDigest != "" && model.ValidateSHA256(gate.ReceiptDigest) != nil) || (gate.ContractDigest != "" && model.ValidateSHA256(gate.ContractDigest) != nil) || (gate.TreeID != "" && model.ValidateCommitSHA(gate.TreeID) != nil) {
			return model.TaskExecutionVerification{}, fmt.Errorf("invalid legacy Task verification gate evidence")
		}
	}
	return receipt, nil
}

// TaskExecutionVerificationReceipt is the raw persisted verification row —
// returned unvalidated so the bounded pre-execution reconciliation can inspect
// a legacy receipt shape the current Procedure-gate contract rejects.
type TaskExecutionVerificationReceipt struct {
	OperationID     string
	Outcome         string
	AttemptRevision int
	ReceiptJSON     string
}

// ReadTaskExecutionVerificationReceipts returns every persisted verification
// receipt for a Task ordered by attempt revision, without contract decoding.
// The bounded reconciliation path validates the exact historical shape itself;
// anything beyond the tiny bound is corrupt and fails closed.
func (d *Databases) ReadTaskExecutionVerificationReceipts(ctx context.Context, projectID, taskID string) ([]TaskExecutionVerificationReceipt, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT operation_id, attempt_revision, outcome, receipt_json FROM local_task_execution_verifications WHERE project_id=? AND task_id=? AND NOT EXISTS(SELECT 1 FROM local_project_retirements WHERE project_id=local_task_execution_verifications.project_id) ORDER BY attempt_revision LIMIT 8`, projectID, taskID)
	if err != nil {
		return nil, err
	}
	receipts := make([]TaskExecutionVerificationReceipt, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid Task verification row")
		}
		op, ok := row[0].(string)
		attempt, okAttempt := row[1].(int64)
		outcome, okOutcome := row[2].(string)
		receiptJSON, okJSON := row[3].(string)
		if !ok || !okAttempt || !okOutcome || !okJSON {
			return nil, fmt.Errorf("invalid Task verification receipt encoding")
		}
		receipts = append(receipts, TaskExecutionVerificationReceipt{
			OperationID:     op,
			AttemptRevision: int(attempt),
			Outcome:         outcome,
			ReceiptJSON:     receiptJSON,
		})
	}
	return receipts, nil
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
		{SQL: `UPDATE local_task_execution_states SET task_revision=?,task_revision_sha256=?,status=?,stage=?,worktree=?,base_head_sha=?,head_sha=?,branch=?,agent=?,execution_revision=?,updated_at=? WHERE project_id=? AND task_id=? AND task_revision=? AND task_revision_sha256=? AND status=? AND stage=? AND worktree=? AND base_head_sha=? AND head_sha=? AND branch=? AND agent=? AND execution_revision=? AND NOT EXISTS(SELECT 1 FROM local_project_retirements WHERE project_id=?)`, Args: []any{nextState.TaskRevision, nextState.TaskRevisionSHA256, nextState.Status, nextState.Stage, nextState.Worktree, nextState.BaseHead, nextState.Head, nextState.Branch, nextState.Agent, nextState.ExecutionRevision, nextState.UpdatedAt.UTC().Format(time.RFC3339Nano), nextState.ProjectID, nextState.TaskID, nextState.TaskRevision, nextState.TaskRevisionSHA256, model.TaskExecutionVerifying, nextState.Stage, nextState.Worktree, nextState.BaseHead, nextState.Head, nextState.Branch, nextState.Agent, expectedRevision, nextState.ProjectID}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO local_task_execution_verifications(project_id,task_id,operation_id,attempt_revision,outcome,receipt_json,created_at) SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM local_project_retirements WHERE project_id=?)`, Args: []any{receipt.ProjectID, receipt.TaskID, receipt.OperationID, receipt.AttemptRevision, receipt.Outcome, receiptJSON, receipt.CompletedAt.UTC().Format(time.RFC3339Nano), receipt.ProjectID}, RequireRowsAffected: 1},
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
	rows, err := d.Local.Query(ctx, `SELECT receipt_json FROM local_task_execution_verifications WHERE project_id=? AND task_id=? AND operation_id=? AND attempt_revision=? AND NOT EXISTS(SELECT 1 FROM local_project_retirements WHERE project_id=local_task_execution_verifications.project_id)`, projectID, taskID, operationID, attemptRevision)
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
