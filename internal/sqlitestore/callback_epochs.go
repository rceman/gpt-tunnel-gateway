package sqlitestore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// CallbackEpoch is the local durable marker for one successfully dispatched
// Agent work epoch. It is intentionally operational state and is never sent
// through the Shared/Hub outbox.
type CallbackEpoch struct {
	ID               string
	ProjectID        string
	AgentID          string
	SessionID        string
	SessionKey       string
	ArmedAt          time.Time
	BusySeen         bool
	IdleObservations int
	OperationID      string
	Outcome          string
	CompletedAt      time.Time
}

func (d *Databases) ArmCallbackEpoch(ctx context.Context, epoch CallbackEpoch) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	if epoch.ID == "" || epoch.ProjectID == "" || epoch.SessionKey == "" || epoch.ArmedAt.IsZero() {
		return fmt.Errorf("Agent-work epoch identity is incomplete")
	}
	_, err := d.Local.Exec(ctx, `INSERT OR IGNORE INTO local_callback_epochs(epoch_id,project_id,agent_id,session_key,armed_at,session_id) VALUES(?,?,?,?,?,?)`, epoch.ID, epoch.ProjectID, epoch.AgentID, epoch.SessionKey, epoch.ArmedAt.UTC().Format(time.RFC3339Nano), epoch.SessionID)
	return err
}

func (d *Databases) PendingCallbackEpochs(ctx context.Context, limit int) ([]CallbackEpoch, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local store is unavailable")
	}
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("invalid Agent-work epoch limit")
	}
	rows, err := d.Local.Query(ctx, `SELECT epoch_id,project_id,agent_id,session_key,armed_at,busy_seen,idle_observations,session_id FROM local_callback_epochs WHERE emitted_at IS NULL ORDER BY armed_at,epoch_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	result := make([]CallbackEpoch, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 8 {
			return nil, fmt.Errorf("invalid Agent-work epoch row")
		}
		id, idOK := row[0].(string)
		projectID, projectOK := row[1].(string)
		agentID, agentOK := row[2].(string)
		sessionKey, sessionOK := row[3].(string)
		armedAt, armedAtOK := row[4].(string)
		busySeen, busyOK := row[5].(int64)
		idleObservations, idleOK := row[6].(int64)
		sessionID, sessionIDOK := row[7].(string)
		parsed, parseErr := time.Parse(time.RFC3339Nano, armedAt)
		if !idOK || !projectOK || !agentOK || !sessionOK || !armedAtOK || !busyOK || !idleOK || !sessionIDOK || parseErr != nil || id == "" || projectID == "" || sessionKey == "" || busySeen < 0 || busySeen > 1 || idleObservations < 0 {
			return nil, fmt.Errorf("invalid Agent-work epoch values")
		}
		result = append(result, CallbackEpoch{
			ID:               id,
			ProjectID:        projectID,
			AgentID:          agentID,
			SessionID:        sessionID,
			SessionKey:       sessionKey,
			ArmedAt:          parsed,
			BusySeen:         busySeen == 1,
			IdleObservations: int(idleObservations),
		})
	}
	return result, nil
}

// ObserveCallbackEpoch records an Agent state sample and reports whether the
// epoch has reached two idle observations after real work was observed. It
// never claims the epoch; the bound post_agent_work_finished Hook claims it
// atomically with allocation of its durable Procedure Operation.
func (d *Databases) ObserveCallbackEpoch(ctx context.Context, epochID, state string) (bool, error) {
	if d == nil || d.Local == nil {
		return false, fmt.Errorf("local store is unavailable")
	}
	if epochID == "" {
		return false, fmt.Errorf("Agent-work epoch ID is required")
	}
	rows, err := d.Local.Query(ctx, `SELECT busy_seen,idle_observations,emitted_at FROM local_callback_epochs WHERE epoch_id=?`, epochID)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) == 0 {
		return false, fmt.Errorf("Agent-work epoch %q: %w", epochID, os.ErrNotExist)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return false, fmt.Errorf("invalid Agent-work epoch state")
	}
	busySeen, busyOK := rows.Rows[0][0].(int64)
	idleObservations, idleOK := rows.Rows[0][1].(int64)
	emittedAt := ""
	if rows.Rows[0][2] != nil {
		value, emittedOK := rows.Rows[0][2].(string)
		if !emittedOK {
			return false, fmt.Errorf("invalid Agent-work epoch emitted state")
		}
		emittedAt = value
	}
	if !busyOK || !idleOK || (busySeen != 0 && busySeen != 1) || idleObservations < 0 {
		return false, fmt.Errorf("invalid Agent-work epoch state values")
	}
	if emittedAt != "" {
		return false, nil
	}
	switch state {
	case "running", "waiting":
		_, err = d.Local.Exec(ctx, `UPDATE local_callback_epochs SET busy_seen=1,idle_observations=0 WHERE epoch_id=? AND emitted_at IS NULL`, epochID)
		return false, err
	case "idle":
		if busySeen == 0 {
			return false, nil
		}
		next := idleObservations + 1
		_, err = d.Local.Exec(ctx, `UPDATE local_callback_epochs SET idle_observations=? WHERE epoch_id=? AND emitted_at IS NULL`, next, epochID)
		return next >= 2, err
	default:
		_, err = d.Local.Exec(ctx, `UPDATE local_callback_epochs SET idle_observations=0 WHERE epoch_id=? AND emitted_at IS NULL`, epochID)
		return false, err
	}
}

var ErrCallbackEpochClaimed = errors.New("agent work epoch is already claimed")

type AgentWorkFinishedHookState struct {
	EpochID     string
	ProjectID   string
	AgentID     string
	OperationID string
	Outcome     string
}

func (d *Databases) AllocateCallbackEpochOperation(ctx context.Context, epochID, projectID, projectCode, sessionID, mutationID, inputSHA256, kind string, now time.Time) (LocalOperation, bool, error) {
	if d == nil || d.Local == nil {
		return LocalOperation{}, false, fmt.Errorf("local store is unavailable")
	}
	if epochID == "" || model.ValidateProjectIdentifier(projectID) != nil || model.ValidateProjectCode(projectCode) != nil || sessionID == "" || kind == "" || now.IsZero() || len(mutationID) != 64 {
		return LocalOperation{}, false, fmt.Errorf("agent work Operation allocation is incomplete")
	}
	if _, err := hex.DecodeString(mutationID); err != nil {
		return LocalOperation{}, false, fmt.Errorf("invalid agent work Operation mutation identity")
	}
	if err := validateAdmissionCoordinate(sessionID, inputSHA256); err != nil {
		return LocalOperation{}, false, err
	}
	at := now.UTC().Format(time.RFC3339Nano)
	_, err := d.Local.Batch(ctx, []upstream.Statement{
		{SQL: `INSERT OR IGNORE INTO local_operation_sequences(project_id,project_code,next_number) VALUES(?,?,1)`, Args: []any{projectID, projectCode}},
		{SQL: `UPDATE local_callback_epochs SET emitted_at=?,operation_id=(SELECT project_code || '-OPR' || CAST(next_number AS TEXT) FROM local_operation_sequences WHERE project_id=? AND project_code=?),hook_outcome='pending' WHERE epoch_id=? AND project_id=? AND session_id=? AND emitted_at IS NULL AND operation_id IS NULL`, Args: []any{at, projectID, projectCode, epochID, projectID, sessionID}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO local_operations(operation_id,project_id,project_code,operation_number,mutation_id,kind,status,result_payload,error,recovery_reason,created_at,updated_at,admission_session_id,admission_input_sha256) SELECT e.operation_id,e.project_id,?,s.next_number,?,?,?,NULL,'','',?,?,?,? FROM local_callback_epochs e JOIN local_operation_sequences s ON s.project_id=e.project_id AND s.project_code=? WHERE e.epoch_id=? AND e.project_id=? AND e.hook_outcome='pending' AND e.operation_id IS NOT NULL`, Args: []any{projectCode, mutationID, kind, "accepted", at, at, sessionID, inputSHA256, projectCode, epochID, projectID}, RequireRowsAffected: 1},
		{SQL: `UPDATE local_operation_sequences SET next_number=next_number+1 WHERE project_id=? AND project_code=? AND EXISTS(SELECT 1 FROM local_callback_epochs WHERE epoch_id=? AND project_id=? AND hook_outcome='pending' AND operation_id IS NOT NULL)`, Args: []any{projectID, projectCode, epochID, projectID}, RequireRowsAffected: 1},
	})
	if err != nil {
		if epoch, readErr := d.ReadCallbackEpoch(ctx, epochID); readErr == nil && epoch.OperationID != "" {
			if operation, opErr := d.ReadLocalOperation(ctx, epoch.OperationID); opErr == nil && operation.ProjectID == projectID && operation.Kind == kind {
				return operation, false, nil
			}
		}
		if epoch, readErr := d.ReadCallbackEpoch(ctx, epochID); readErr == nil && epoch.OperationID == "" && !epoch.ArmedAt.IsZero() {
			return LocalOperation{}, false, ErrCallbackEpochClaimed
		}
		return LocalOperation{}, false, err
	}
	operation, err := d.ReadLocalOperationByMutation(ctx, mutationID)
	return operation, err == nil, err
}

func (d *Databases) ClaimCallbackEpochWithoutHook(ctx context.Context, epochID string, claimedAt time.Time) (bool, error) {
	if d == nil || d.Local == nil || epochID == "" || claimedAt.IsZero() {
		return false, fmt.Errorf("agent work epoch claim is incomplete")
	}
	result, err := d.Local.Exec(ctx, `UPDATE local_callback_epochs SET emitted_at=?,hook_outcome='unbound',hook_completed_at=? WHERE epoch_id=? AND emitted_at IS NULL AND operation_id IS NULL`, claimedAt.UTC().Format(time.RFC3339Nano), claimedAt.UTC().Format(time.RFC3339Nano), epochID)
	if err != nil {
		return false, err
	}
	return result.RowsAffected == 1, nil
}

func (d *Databases) ReadCallbackEpoch(ctx context.Context, epochID string) (CallbackEpoch, error) {
	if d == nil || d.Local == nil || epochID == "" {
		return CallbackEpoch{}, fmt.Errorf("agent work epoch identity is required")
	}
	rows, err := d.Local.Query(ctx, `SELECT epoch_id,project_id,agent_id,session_id,session_key,armed_at,busy_seen,idle_observations,emitted_at,operation_id,hook_outcome,hook_completed_at FROM local_callback_epochs WHERE epoch_id=?`, epochID)
	if err != nil {
		return CallbackEpoch{}, err
	}
	if len(rows.Rows) == 0 {
		return CallbackEpoch{}, fmt.Errorf("agent work epoch %q: %w", epochID, os.ErrNotExist)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 12 {
		return CallbackEpoch{}, fmt.Errorf("invalid agent work epoch row")
	}
	row := rows.Rows[0]
	stringAt := func(index int) (string, error) {
		value, ok := row[index].(string)
		if !ok {
			return "", fmt.Errorf("invalid agent work epoch text value")
		}
		return value, nil
	}
	id, err := stringAt(0)
	if err != nil {
		return CallbackEpoch{}, err
	}
	projectID, err := stringAt(1)
	if err != nil {
		return CallbackEpoch{}, err
	}
	agentID, err := stringAt(2)
	if err != nil {
		return CallbackEpoch{}, err
	}
	sessionID, err := stringAt(3)
	if err != nil {
		return CallbackEpoch{}, err
	}
	sessionKey, err := stringAt(4)
	if err != nil {
		return CallbackEpoch{}, err
	}
	armedAtText, err := stringAt(5)
	if err != nil {
		return CallbackEpoch{}, err
	}
	busySeen, busyOK := row[6].(int64)
	idleObservations, idleOK := row[7].(int64)
	armedAt, parseErr := time.Parse(time.RFC3339Nano, armedAtText)
	if !busyOK || !idleOK || parseErr != nil || busySeen < 0 || busySeen > 1 || idleObservations < 0 {
		return CallbackEpoch{}, fmt.Errorf("invalid agent work epoch values")
	}
	result := CallbackEpoch{
		ID:               id,
		ProjectID:        projectID,
		AgentID:          agentID,
		SessionID:        sessionID,
		SessionKey:       sessionKey,
		ArmedAt:          armedAt,
		BusySeen:         busySeen == 1,
		IdleObservations: int(idleObservations),
	}
	if row[8] != nil {
		if _, err := stringAt(8); err != nil {
			return CallbackEpoch{}, err
		}
	}
	if row[9] != nil {
		result.OperationID, err = stringAt(9)
		if err != nil {
			return CallbackEpoch{}, err
		}
	}
	result.Outcome, err = stringAt(10)
	if err != nil {
		return CallbackEpoch{}, err
	}
	if row[11] != nil {
		completedAtText, err := stringAt(11)
		if err != nil {
			return CallbackEpoch{}, err
		}
		result.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAtText)
		if err != nil {
			return CallbackEpoch{}, fmt.Errorf("invalid agent work epoch completion time")
		}
	}
	return result, nil
}

func (d *Databases) RecordCallbackEpochOperationOutcome(ctx context.Context, epochID, operationID, outcome string, completedAt time.Time) error {
	if d == nil || d.Local == nil || epochID == "" || model.ValidateOperationID(operationID) != nil || outcome != "completed" && outcome != "failed" && outcome != "outcome_unknown" || completedAt.IsZero() {
		return fmt.Errorf("invalid Agent-work Hook outcome")
	}
	result, err := d.Local.Exec(ctx, `UPDATE local_callback_epochs SET hook_outcome=?,hook_completed_at=? WHERE epoch_id=? AND operation_id=? AND emitted_at IS NOT NULL`, outcome, completedAt.UTC().Format(time.RFC3339Nano), epochID, operationID)
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("Agent-work Hook Operation association does not match")
	}
	return nil
}

func (d *Databases) LatestAgentWorkFinishedHook(ctx context.Context, projectID, agentID string) (AgentWorkFinishedHookState, bool, error) {
	if d == nil || d.Local == nil || model.ValidateProjectIdentifier(projectID) != nil || agentID == "" {
		return AgentWorkFinishedHookState{}, false, fmt.Errorf("invalid Agent-work Hook status selector")
	}
	rows, err := d.Local.Query(ctx, `SELECT epoch_id,project_id,agent_id,operation_id,hook_outcome FROM local_callback_epochs WHERE project_id=? AND agent_id=? AND operation_id IS NOT NULL ORDER BY armed_at DESC,epoch_id DESC LIMIT 1`, projectID, agentID)
	if err != nil {
		return AgentWorkFinishedHookState{}, false, err
	}
	if len(rows.Rows) == 0 {
		return AgentWorkFinishedHookState{}, false, nil
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 5 {
		return AgentWorkFinishedHookState{}, false, fmt.Errorf("invalid Agent-work Hook status row")
	}
	values := make([]string, 5)
	for index, value := range rows.Rows[0] {
		if value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return AgentWorkFinishedHookState{}, false, fmt.Errorf("invalid Agent-work Hook status value")
		}
		values[index] = text
	}
	if values[0] == "" || values[1] != projectID || values[2] != agentID || model.ValidateOperationID(values[3]) != nil {
		return AgentWorkFinishedHookState{}, false, fmt.Errorf("invalid Agent-work Hook status identity")
	}
	return AgentWorkFinishedHookState{
		EpochID:     values[0],
		ProjectID:   values[1],
		AgentID:     values[2],
		OperationID: values[3],
		Outcome:     values[4],
	}, true, nil
}
