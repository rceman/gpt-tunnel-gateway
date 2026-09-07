package sqlitestore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func (d *Databases) CommitSharedADRCreate(ctx context.Context, request SharedADRCreate) (SharedMutationReceipt, string, []byte, error) {
	if d == nil || d.Shared == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared store is unavailable")
	}
	if request.OperationID == "" || request.ProjectID == "" || request.Kind == "" || request.BuildPayload == nil {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared ADR create identity is incomplete")
	}
	if len(request.ProjectCode) != 3 || strings.ToUpper(request.ProjectCode) != request.ProjectCode {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("invalid shared ADR project code")
	}
	created := request.CreatedAt.UTC().Format(time.RFC3339Nano)
	if request.CreatedAt.IsZero() {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if existing, found, err := d.outboxEntry(ctx, request.OperationID); err != nil {
		return SharedMutationReceipt{}, "", nil, err
	} else if found {
		receipt, reuseErr := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "adr", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
		return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
	}
	next, err := d.nextADRNumber(ctx, request.ProjectID, request.ProjectCode, request.InitialNextADRNumber)
	if err != nil {
		return SharedMutationReceipt{}, "", nil, err
	}
	id := fmt.Sprintf("%s-ADR%d", request.ProjectCode, next)
	payload, err := request.BuildPayload(id)
	if err != nil {
		return SharedMutationReceipt{}, "", nil, err
	}
	if len(payload) == 0 {
		return SharedMutationReceipt{}, "", nil, fmt.Errorf("shared ADR payload is empty")
	}
	_, err = d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_adr_sequences SET next_adr_number=? WHERE project_id=? AND project_code=? AND next_adr_number=?`, Args: []any{next + 1, request.ProjectID, request.ProjectCode, next}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{id, 1, payload, created}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{request.OperationID, "adr", id, 1, request.Kind, payload, created}, RequireRowsAffected: 1},
	})
	if err != nil {
		if existing, found, readErr := d.outboxEntry(ctx, request.OperationID); readErr == nil && found {
			receipt, reuseErr := d.reuseSharedMutation(SharedMutation{OperationID: request.OperationID, EntityType: "adr", EntityID: existing.EntityID, Revision: existing.Revision, Kind: request.Kind, Payload: existing.Payload}, existing)
			return receipt, existing.EntityID, append([]byte(nil), existing.Payload...), reuseErr
		}
		return SharedMutationReceipt{}, "", nil, err
	}
	return SharedMutationReceipt{OperationID: request.OperationID, EntityType: "adr", EntityID: id, Revision: 1, Committed: true}, id, payload, nil
}

func (d *Databases) nextADRNumber(ctx context.Context, projectID, projectCode string, initial int64) (int64, error) {
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_adr_number FROM shared_adr_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) == 0 {
		if initial < 1 {
			initial = 1
		}
		if _, err := d.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_adr_sequences(project_id,project_code,next_adr_number) VALUES(?,?,?)`, projectID, projectCode, initial); err != nil {
			return 0, err
		}
		rows, err = d.Shared.Query(ctx, `SELECT project_code,next_adr_number FROM shared_adr_sequences WHERE project_id=?`, projectID)
		if err != nil {
			return 0, err
		}
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode {
		return 0, fmt.Errorf("shared ADR project code mismatch")
	}
	next, ok := rows.Rows[0][1].(int64)
	if !ok || next < 1 {
		return 0, fmt.Errorf("invalid shared ADR sequence")
	}
	return next, nil
}

func (d *Databases) PutSharedADRSequence(ctx context.Context, projectID, projectCode string, next int64) error {
	if d == nil || d.Shared == nil || projectID == "" || len(projectCode) != 3 || strings.ToUpper(projectCode) != projectCode || next < 1 {
		return fmt.Errorf("invalid shared ADR sequence")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_adr_sequences(project_id,project_code,next_adr_number) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET project_code=excluded.project_code,next_adr_number=CASE WHEN excluded.next_adr_number > shared_adr_sequences.next_adr_number THEN excluded.next_adr_number ELSE shared_adr_sequences.next_adr_number END`, projectID, projectCode, next)
	return err
}

func (d *Databases) nextTaskNumber(ctx context.Context, projectID, projectCode string, initial int64) (int64, error) {
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_task_number FROM shared_task_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) == 0 {
		if initial < 1 {
			initial = 1
		}
		if _, err := d.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_task_sequences(project_id,project_code,next_task_number) VALUES(?,?,?)`, projectID, projectCode, initial); err != nil {
			return 0, err
		}
		rows, err = d.Shared.Query(ctx, `SELECT project_code,next_task_number FROM shared_task_sequences WHERE project_id=?`, projectID)
		if err != nil {
			return 0, err
		}
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != projectCode {
		return 0, fmt.Errorf("shared task project code mismatch")
	}
	next, ok := rows.Rows[0][1].(int64)
	if !ok || next < 1 {
		return 0, fmt.Errorf("invalid shared task sequence")
	}
	return next, nil
}

func (d *Databases) ReadSharedTaskSequence(ctx context.Context, projectID string) (string, int64, bool, error) {
	if d == nil || d.Shared == nil {
		return "", 0, false, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_code,next_task_number FROM shared_task_sequences WHERE project_id=?`, projectID)
	if err != nil {
		return "", 0, false, err
	}
	if len(rows.Rows) == 0 {
		return "", 0, false, nil
	}
	if len(rows.Rows) != 1 {
		return "", 0, false, fmt.Errorf("invalid shared task sequence")
	}
	code, codeOK := rows.Rows[0][0].(string)
	next, nextOK := rows.Rows[0][1].(int64)
	if !codeOK || !nextOK || next < 1 {
		return "", 0, false, fmt.Errorf("invalid shared task sequence")
	}
	return code, next, true, nil
}

func (d *Databases) PutSharedTaskSequence(ctx context.Context, projectID, projectCode string, next int64) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if projectID == "" || len(projectCode) != 3 || strings.ToUpper(projectCode) != projectCode || next < 1 {
		return fmt.Errorf("invalid shared task sequence")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_task_sequences(project_id,project_code,next_task_number) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET project_code=excluded.project_code,next_task_number=CASE WHEN excluded.next_task_number > shared_task_sequences.next_task_number THEN excluded.next_task_number ELSE shared_task_sequences.next_task_number END`, projectID, projectCode, next)
	return err
}

func (d *Databases) MarkSharedBootstrapComplete(ctx context.Context, marker SharedBootstrapMarker) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	if marker.ProjectID == "" || marker.HubRevision == "" || marker.CompletedAt == "" {
		return fmt.Errorf("invalid shared bootstrap marker")
	}
	_, err := d.Shared.Exec(ctx, `INSERT INTO shared_bootstrap_markers(project_id,hub_revision,completed_at) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET hub_revision=excluded.hub_revision,completed_at=excluded.completed_at`, marker.ProjectID, marker.HubRevision, marker.CompletedAt)
	return err
}

func (d *Databases) SharedBootstrapComplete(ctx context.Context, projectID string) (bool, error) {
	if d == nil || d.Shared == nil {
		return false, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_id,hub_revision,completed_at FROM shared_bootstrap_markers WHERE project_id=?`, projectID)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) == 0 {
		return false, nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return false, fmt.Errorf("invalid shared bootstrap marker")
	}
	project, projectOK := rows.Rows[0][0].(string)
	revision, revisionOK := rows.Rows[0][1].(string)
	completed, completedOK := rows.Rows[0][2].(string)
	if !projectOK || !revisionOK || !completedOK || project != projectID || revision == "" || completed == "" {
		return false, fmt.Errorf("invalid shared bootstrap marker")
	}
	return true, nil
}

func (d *Databases) ReadSharedTask(ctx context.Context, taskID string) (SharedTask, error) {
	if d == nil || d.Shared == nil {
		return SharedTask{}, fmt.Errorf("shared store is unavailable")
	}
	rows, err := d.Shared.Query(ctx, `SELECT id,revision,payload,updated_at FROM shared_tasks WHERE id=?`, taskID)
	if err != nil {
		return SharedTask{}, err
	}
	if len(rows.Rows) == 0 {
		return SharedTask{}, fmt.Errorf("shared task %q: %w", taskID, os.ErrNotExist)
	}
	if len(rows.Rows[0]) != 4 {
		return SharedTask{}, fmt.Errorf("invalid shared task row")
	}
	id, idOK := rows.Rows[0][0].(string)
	revision, revisionOK := rows.Rows[0][1].(int64)
	payload, payloadOK := rows.Rows[0][2].([]byte)
	updatedAt, updatedOK := rows.Rows[0][3].(string)
	if !idOK || !revisionOK || !payloadOK || !updatedOK {
		return SharedTask{}, fmt.Errorf("invalid shared task row")
	}
	return SharedTask{ID: id, Revision: revision, Payload: append([]byte(nil), payload...), UpdatedAt: updatedAt}, nil
}
