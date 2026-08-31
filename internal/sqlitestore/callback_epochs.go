package sqlitestore

import (
	"context"
	"fmt"
	"os"
	"time"
)

// CallbackEpoch is the local durable marker for one successfully dispatched
// Agent work epoch. It is intentionally operational state and is never sent
// through the Shared/Hub outbox.
type CallbackEpoch struct {
	ID               string
	ProjectID        string
	AgentID          string
	SessionKey       string
	ArmedAt          time.Time
	BusySeen         bool
	IdleObservations int
}

func (d *Databases) ArmCallbackEpoch(ctx context.Context, epoch CallbackEpoch) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	if epoch.ID == "" || epoch.ProjectID == "" || epoch.SessionKey == "" || epoch.ArmedAt.IsZero() {
		return fmt.Errorf("callback epoch identity is incomplete")
	}
	_, err := d.Local.Exec(ctx, `INSERT OR IGNORE INTO local_callback_epochs(epoch_id,project_id,agent_id,session_key,armed_at) VALUES(?,?,?,?,?)`, epoch.ID, epoch.ProjectID, epoch.AgentID, epoch.SessionKey, epoch.ArmedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (d *Databases) PendingCallbackEpochs(ctx context.Context, limit int) ([]CallbackEpoch, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local store is unavailable")
	}
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("invalid callback epoch limit")
	}
	rows, err := d.Local.Query(ctx, `SELECT epoch_id,project_id,agent_id,session_key,armed_at,busy_seen,idle_observations FROM local_callback_epochs WHERE emitted_at IS NULL ORDER BY armed_at,epoch_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	result := make([]CallbackEpoch, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 7 {
			return nil, fmt.Errorf("invalid callback epoch row")
		}
		id, idOK := row[0].(string)
		projectID, projectOK := row[1].(string)
		agentID, agentOK := row[2].(string)
		sessionKey, sessionOK := row[3].(string)
		armedAt, armedAtOK := row[4].(string)
		busySeen, busyOK := row[5].(int64)
		idleObservations, idleOK := row[6].(int64)
		parsed, parseErr := time.Parse(time.RFC3339Nano, armedAt)
		if !idOK || !projectOK || !agentOK || !sessionOK || !armedAtOK || !busyOK || !idleOK || parseErr != nil || id == "" || projectID == "" || sessionKey == "" || busySeen < 0 || busySeen > 1 || idleObservations < 0 {
			return nil, fmt.Errorf("invalid callback epoch values")
		}
		result = append(result, CallbackEpoch{ID: id, ProjectID: projectID, AgentID: agentID, SessionKey: sessionKey, ArmedAt: parsed, BusySeen: busySeen == 1, IdleObservations: int(idleObservations)})
	}
	return result, nil
}

// ObserveCallbackEpoch records an Agent state sample and reports whether the
// epoch has reached two idle observations after real work was observed. It
// never marks an epoch emitted; ClaimCallbackEpoch does that after the
// configured callbacks have been read and before delivery starts.
func (d *Databases) ObserveCallbackEpoch(ctx context.Context, epochID, state string) (bool, error) {
	if d == nil || d.Local == nil {
		return false, fmt.Errorf("local store is unavailable")
	}
	if epochID == "" {
		return false, fmt.Errorf("callback epoch ID is required")
	}
	rows, err := d.Local.Query(ctx, `SELECT busy_seen,idle_observations,emitted_at FROM local_callback_epochs WHERE epoch_id=?`, epochID)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) == 0 {
		return false, fmt.Errorf("callback epoch %q: %w", epochID, os.ErrNotExist)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 3 {
		return false, fmt.Errorf("invalid callback epoch state")
	}
	busySeen, busyOK := rows.Rows[0][0].(int64)
	idleObservations, idleOK := rows.Rows[0][1].(int64)
	emittedAt := ""
	if rows.Rows[0][2] != nil {
		value, emittedOK := rows.Rows[0][2].(string)
		if !emittedOK {
			return false, fmt.Errorf("invalid callback epoch emitted state")
		}
		emittedAt = value
	}
	if !busyOK || !idleOK || (busySeen != 0 && busySeen != 1) || idleObservations < 0 {
		return false, fmt.Errorf("invalid callback epoch state values")
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

func (d *Databases) ClaimCallbackEpoch(ctx context.Context, epochID string, emittedAt time.Time) (bool, error) {
	if d == nil || d.Local == nil {
		return false, fmt.Errorf("local store is unavailable")
	}
	if epochID == "" || emittedAt.IsZero() {
		return false, fmt.Errorf("callback epoch claim is incomplete")
	}
	result, err := d.Local.Exec(ctx, `UPDATE local_callback_epochs SET emitted_at=? WHERE epoch_id=? AND emitted_at IS NULL`, emittedAt.UTC().Format(time.RFC3339Nano), epochID)
	if err != nil {
		return false, err
	}
	return result.RowsAffected == 1, nil
}
