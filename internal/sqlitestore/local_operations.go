package sqlitestore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type LocalOperation struct {
	OperationID          string
	ProjectID            string
	ProjectCode          string
	OperationNumber      uint64
	MutationID           string
	Kind                 string
	Status               string
	ResultPayload        []byte
	Error                string
	RecoveryReason       string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	AdmissionSessionID   string
	AdmissionInputSHA256 string
}

type LocalOperationTurnSummary struct {
	OperationID string
	ProjectID   string
	Kind        string
	Status      string
}

func (d *Databases) AllocateLocalOperation(ctx context.Context, projectID, projectCode, mutationID, kind string, now time.Time) (LocalOperation, error) {
	return d.allocateLocalOperation(ctx, projectID, projectCode, mutationID, kind, "", "", now)
}

func (d *Databases) AllocateLocalOperationWithAdmissionCoordinate(ctx context.Context, projectID, projectCode, mutationID, kind, sessionID, inputSHA256 string, now time.Time) (LocalOperation, error) {
	return d.allocateLocalOperation(ctx, projectID, projectCode, mutationID, kind, sessionID, inputSHA256, now)
}

func (d *Databases) allocateLocalOperation(ctx context.Context, projectID, projectCode, mutationID, kind, sessionID, inputSHA256 string, now time.Time) (LocalOperation, error) {
	if d == nil || d.Local == nil {
		return LocalOperation{}, fmt.Errorf("local store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return LocalOperation{}, err
	}
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return LocalOperation{}, err
	}
	if len(mutationID) != 64 {
		return LocalOperation{}, fmt.Errorf("invalid operation mutation identity")
	}
	if _, err := hex.DecodeString(mutationID); err != nil {
		return LocalOperation{}, fmt.Errorf("invalid operation mutation identity: %w", err)
	}
	if kind == "" || now.IsZero() {
		return LocalOperation{}, fmt.Errorf("invalid local operation allocation")
	}
	if err := validateAdmissionCoordinate(sessionID, inputSHA256); err != nil {
		return LocalOperation{}, err
	}
	if existing, err := d.ReadLocalOperationByMutation(ctx, mutationID); err == nil {
		if existing.ProjectID != projectID || existing.ProjectCode != projectCode || existing.Kind != kind {
			return LocalOperation{}, fmt.Errorf("local operation mutation identity mismatch")
		}
		if inputSHA256 != "" && (existing.AdmissionSessionID != sessionID || existing.AdmissionInputSHA256 != inputSHA256) {
			if existing.AdmissionSessionID != "" || existing.AdmissionInputSHA256 != "" {
				return LocalOperation{}, fmt.Errorf("local operation admission coordinate mismatch")
			}
			if err := d.SetLocalOperationAdmissionCoordinate(ctx, existing.OperationID, projectID, sessionID, inputSHA256); err != nil {
				return LocalOperation{}, err
			}
			return d.ReadLocalOperation(ctx, existing.OperationID)
		}
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return LocalOperation{}, err
	}
	at := now.UTC().Format(time.RFC3339Nano)
	_, err := d.Local.Batch(ctx, []upstream.Statement{
		{SQL: `INSERT OR IGNORE INTO local_operation_sequences(project_id,project_code,next_number) VALUES(?,?,1)`, Args: []any{projectID, projectCode}},
		{SQL: `INSERT INTO local_operations(operation_id,project_id,project_code,operation_number,mutation_id,kind,status,result_payload,error,recovery_reason,created_at,updated_at,admission_session_id,admission_input_sha256) SELECT project_code || '-OPR' || CAST(next_number AS TEXT),project_id,project_code,next_number,?,?,?,NULL,'','',?,?,?,? FROM local_operation_sequences WHERE project_id=? AND project_code=?`, Args: []any{mutationID, kind, "accepted", at, at, sessionID, inputSHA256, projectID, projectCode}, RequireRowsAffected: 1},
		{SQL: `UPDATE local_operation_sequences SET next_number=next_number+1 WHERE project_id=? AND project_code=?`, Args: []any{projectID, projectCode}, RequireRowsAffected: 1},
	})
	if err != nil {
		if existing, readErr := d.ReadLocalOperationByMutation(ctx, mutationID); readErr == nil {
			if inputSHA256 != "" && (existing.AdmissionSessionID != sessionID || existing.AdmissionInputSHA256 != inputSHA256) {
				return LocalOperation{}, fmt.Errorf("local operation admission coordinate mismatch")
			}
			return existing, nil
		}
		return LocalOperation{}, err
	}
	return d.ReadLocalOperationByMutation(ctx, mutationID)
}

func (d *Databases) ReadLocalOperation(ctx context.Context, operationID string) (LocalOperation, error) {
	if d == nil || d.Local == nil || model.ValidateOperationID(operationID) != nil {
		return LocalOperation{}, fmt.Errorf("invalid local operation identifier")
	}
	rows, err := d.Local.Query(ctx, localOperationSelect+` WHERE operation_id=?`, operationID)
	if err != nil {
		return LocalOperation{}, err
	}
	if len(rows.Rows) == 0 {
		return LocalOperation{}, fmt.Errorf("local operation %q: %w", operationID, os.ErrNotExist)
	}
	return decodeLocalOperation(rows.Rows[0])
}

func (d *Databases) ReadLocalOperationByMutation(ctx context.Context, mutationID string) (LocalOperation, error) {
	if d == nil || d.Local == nil || len(mutationID) != 64 {
		return LocalOperation{}, fmt.Errorf("invalid local operation mutation identity")
	}
	if _, err := hex.DecodeString(mutationID); err != nil {
		return LocalOperation{}, fmt.Errorf("invalid local operation mutation identity: %w", err)
	}
	rows, err := d.Local.Query(ctx, localOperationSelect+` WHERE mutation_id=?`, mutationID)
	if err != nil {
		return LocalOperation{}, err
	}
	if len(rows.Rows) == 0 {
		return LocalOperation{}, fmt.Errorf("local operation mutation %q: %w", mutationID, os.ErrNotExist)
	}
	return decodeLocalOperation(rows.Rows[0])
}

func (d *Databases) ListLocalOperations(ctx context.Context, projectID string) ([]LocalOperation, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	rows, err := d.Local.Query(ctx, localOperationSelect+` WHERE project_id=? ORDER BY operation_number DESC`, projectID)
	if err != nil {
		return nil, err
	}
	operations := make([]LocalOperation, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		operation, decodeErr := decodeLocalOperation(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func (d *Databases) ListLocalOperationTurnSummaries(ctx context.Context, projectID string) ([]LocalOperationTurnSummary, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	rows, err := d.Local.Query(ctx, `SELECT operation_id,project_id,kind,status FROM local_operations WHERE project_id=? ORDER BY operation_id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	summaries := make([]LocalOperationTurnSummary, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid local operation turn row")
		}
		summary := LocalOperationTurnSummary{}
		var ok bool
		if summary.OperationID, ok = row[0].(string); !ok || model.ValidateOperationID(summary.OperationID) != nil {
			return nil, fmt.Errorf("invalid local operation turn identifier")
		}
		if summary.ProjectID, ok = row[1].(string); !ok || summary.ProjectID != projectID {
			return nil, fmt.Errorf("invalid local operation turn project")
		}
		if summary.Kind, ok = row[2].(string); !ok || summary.Kind == "" {
			return nil, fmt.Errorf("invalid local operation turn kind")
		}
		if summary.Status, ok = row[3].(string); !ok {
			return nil, fmt.Errorf("invalid local operation turn status")
		}
		if err := validateLocalOperationStatus(summary.Status); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (d *Databases) SetLocalOperationAdmissionCoordinate(ctx context.Context, operationID, projectID, sessionID, inputSHA256 string) error {
	if d == nil || d.Local == nil || model.ValidateOperationID(operationID) != nil || model.ValidateProjectIdentifier(projectID) != nil {
		return fmt.Errorf("invalid local operation admission coordinate")
	}
	if err := validateAdmissionCoordinate(sessionID, inputSHA256); err != nil {
		return err
	}
	existing, err := d.ReadLocalOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if existing.ProjectID != projectID {
		return fmt.Errorf("local operation admission project mismatch")
	}
	if existing.AdmissionSessionID == sessionID && existing.AdmissionInputSHA256 == inputSHA256 {
		return nil
	}
	if existing.AdmissionSessionID != "" || existing.AdmissionInputSHA256 != "" {
		return fmt.Errorf("local operation admission coordinate mismatch")
	}
	_, err = d.Local.Batch(ctx, []upstream.Statement{{SQL: `UPDATE local_operations SET admission_session_id=?,admission_input_sha256=? WHERE operation_id=? AND project_id=? AND admission_session_id='' AND admission_input_sha256=''`, Args: []any{sessionID, inputSHA256, operationID, projectID}, RequireRowsAffected: 1}})
	return err
}

func (d *Databases) ListLocalOperationsByAdmissionCoordinate(ctx context.Context, projectID, kind, sessionID, inputSHA256 string) ([]LocalOperation, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local store is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	if kind == "" {
		return nil, fmt.Errorf("invalid local operation kind")
	}
	if err := validateAdmissionCoordinate(sessionID, inputSHA256); err != nil {
		return nil, err
	}
	rows, err := d.Local.Query(ctx, localOperationSelect+` WHERE project_id=? AND kind=? AND admission_session_id=? AND admission_input_sha256=? ORDER BY operation_number DESC`, projectID, kind, sessionID, inputSHA256)
	if err != nil {
		return nil, err
	}
	operations := make([]LocalOperation, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		operation, decodeErr := decodeLocalOperation(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func (d *Databases) UpdateLocalOperation(ctx context.Context, operation LocalOperation) error {
	if d == nil || d.Local == nil || model.ValidateOperationID(operation.OperationID) != nil || operation.ProjectID == "" || operation.Kind == "" || operation.CreatedAt.IsZero() || operation.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid local operation")
	}
	if err := validateLocalOperationStatus(operation.Status); err != nil {
		return err
	}
	if err := validateAdmissionCoordinate(operation.AdmissionSessionID, operation.AdmissionInputSHA256); err != nil {
		return err
	}
	_, err := d.Local.Batch(ctx, []upstream.Statement{{SQL: `UPDATE local_operations SET status=?,result_payload=?,error=?,recovery_reason=?,updated_at=?,admission_session_id=?,admission_input_sha256=? WHERE operation_id=? AND project_id=?`, Args: []any{operation.Status, operation.ResultPayload, operation.Error, operation.RecoveryReason, operation.UpdatedAt.UTC().Format(time.RFC3339Nano), operation.AdmissionSessionID, operation.AdmissionInputSHA256, operation.OperationID, operation.ProjectID}, RequireRowsAffected: 1}})
	return err
}

const localOperationSelect = `SELECT operation_id,project_id,project_code,operation_number,mutation_id,kind,status,result_payload,error,recovery_reason,created_at,updated_at,admission_session_id,admission_input_sha256 FROM local_operations`

func decodeLocalOperation(row []any) (LocalOperation, error) {
	if len(row) != 14 {
		return LocalOperation{}, fmt.Errorf("invalid local operation row")
	}
	operationNumber, ok := row[3].(int64)
	if !ok || operationNumber < 1 {
		return LocalOperation{}, fmt.Errorf("invalid local operation number")
	}
	operation := LocalOperation{}
	if operation.OperationID, ok = row[0].(string); !ok || model.ValidateOperationID(operation.OperationID) != nil {
		return LocalOperation{}, fmt.Errorf("invalid local operation identifier")
	}
	if operation.ProjectID, ok = row[1].(string); !ok || model.ValidateProjectIdentifier(operation.ProjectID) != nil {
		return LocalOperation{}, fmt.Errorf("invalid local operation project")
	}
	if operation.ProjectCode, ok = row[2].(string); !ok || model.ValidateProjectCode(operation.ProjectCode) != nil {
		return LocalOperation{}, fmt.Errorf("invalid local operation project code")
	}
	operation.OperationNumber = uint64(operationNumber)
	if operation.MutationID, ok = row[4].(string); !ok || len(operation.MutationID) != 64 {
		return LocalOperation{}, fmt.Errorf("invalid local operation mutation")
	}
	if operation.Kind, ok = row[5].(string); !ok || operation.Kind == "" {
		return LocalOperation{}, fmt.Errorf("invalid local operation kind")
	}
	if operation.Status, ok = row[6].(string); !ok {
		return LocalOperation{}, fmt.Errorf("invalid local operation status")
	}
	if err := validateLocalOperationStatus(operation.Status); err != nil {
		return LocalOperation{}, err
	}
	if row[7] != nil {
		payload, payloadOK := row[7].([]byte)
		if !payloadOK {
			return LocalOperation{}, fmt.Errorf("invalid local operation result")
		}
		operation.ResultPayload = append([]byte(nil), payload...)
	}
	if operation.Error, ok = row[8].(string); !ok {
		return LocalOperation{}, fmt.Errorf("invalid local operation error")
	}
	if operation.RecoveryReason, ok = row[9].(string); !ok {
		return LocalOperation{}, fmt.Errorf("invalid local operation recovery reason")
	}
	created, createdOK := row[10].(string)
	updated, updatedOK := row[11].(string)
	if !createdOK || !updatedOK {
		return LocalOperation{}, fmt.Errorf("invalid local operation timestamps")
	}
	var err error
	if operation.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return LocalOperation{}, fmt.Errorf("invalid local operation created_at: %w", err)
	}
	if operation.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return LocalOperation{}, fmt.Errorf("invalid local operation updated_at: %w", err)
	}
	if operation.AdmissionSessionID, ok = row[12].(string); !ok {
		return LocalOperation{}, fmt.Errorf("invalid local operation admission session")
	}
	if operation.AdmissionInputSHA256, ok = row[13].(string); !ok {
		return LocalOperation{}, fmt.Errorf("invalid local operation admission input")
	}
	if err := validateAdmissionCoordinate(operation.AdmissionSessionID, operation.AdmissionInputSHA256); err != nil {
		return LocalOperation{}, err
	}
	return operation, nil
}

func validateAdmissionCoordinate(sessionID, inputSHA256 string) error {
	if sessionID != "" && len(sessionID) > 128 {
		return fmt.Errorf("invalid local operation admission session")
	}
	if sessionID != "" && strings.ContainsRune(sessionID, 0) {
		return fmt.Errorf("invalid local operation admission session")
	}
	if inputSHA256 != "" {
		if len(inputSHA256) != 64 {
			return fmt.Errorf("invalid local operation admission input")
		}
		if _, err := hex.DecodeString(inputSHA256); err != nil {
			return fmt.Errorf("invalid local operation admission input: %w", err)
		}
	}
	if inputSHA256 == "" && sessionID != "" {
		return fmt.Errorf("invalid local operation admission coordinate")
	}
	return nil
}

func validateLocalOperationStatus(status string) error {
	switch status {
	case "accepted", "running", "completed", "failed", "outcome_unknown":
		return nil
	default:
		return fmt.Errorf("invalid local operation status %q", status)
	}
}
