package sqlitestore

import (
	"context"
	"errors"
	"fmt"

	upstream "github.com/rceman/go-sqlite-store/store"
)

var (
	ErrLocalSessionNotFound = errors.New("local session not found")
	ErrLocalSessionExists   = errors.New("local session already exists")
	ErrLocalSessionChanged  = errors.New("local session changed concurrently")
)

// LocalSession is the typed Local operational boundary for durable Session
// records. The session package owns validation and payload encoding.
type LocalSession struct {
	ID        string
	Payload   []byte
	UpdatedAt string
	Status    string
}

func (d *Databases) ReadLocalSession(ctx context.Context, id string) (LocalSession, error) {
	if d == nil || d.Local == nil {
		return LocalSession{}, fmt.Errorf("local session store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT session_id,payload,updated_at,status FROM local_sessions WHERE session_id=?`, id)
	if err != nil {
		return LocalSession{}, err
	}
	if len(rows.Rows) == 0 {
		return LocalSession{}, ErrLocalSessionNotFound
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 4 {
		return LocalSession{}, fmt.Errorf("invalid local session row")
	}
	payload, ok := rows.Rows[0][1].([]byte)
	if !ok {
		return LocalSession{}, fmt.Errorf("invalid local session payload")
	}
	idValue, okID := rows.Rows[0][0].(string)
	updated, okUpdated := rows.Rows[0][2].(string)
	status, okStatus := rows.Rows[0][3].(string)
	if !okID || !okUpdated || !okStatus {
		return LocalSession{}, fmt.Errorf("invalid local session row")
	}
	return LocalSession{
		ID:        idValue,
		Payload:   append([]byte(nil), payload...),
		UpdatedAt: updated,
		Status:    status,
	}, nil
}

func (d *Databases) ListLocalSessions(ctx context.Context) ([]LocalSession, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local session store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT session_id,payload,updated_at,status FROM local_sessions ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	result := make([]LocalSession, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid local session row")
		}
		id, okID := row[0].(string)
		payload, okPayload := row[1].([]byte)
		updated, okUpdated := row[2].(string)
		status, okStatus := row[3].(string)
		if !okID || !okPayload || !okUpdated || !okStatus {
			return nil, fmt.Errorf("invalid local session row")
		}
		result = append(result, LocalSession{
			ID:        id,
			Payload:   append([]byte(nil), payload...),
			UpdatedAt: updated,
			Status:    status,
		})
	}
	return result, nil
}

func (d *Databases) CreateLocalSession(ctx context.Context, record LocalSession) error {
	return d.CreateLocalSessions(ctx, []LocalSession{record})
}

func (d *Databases) CreateLocalSessions(ctx context.Context, records []LocalSession) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local session store is unavailable")
	}
	if len(records) == 0 {
		return nil
	}
	statements := make([]upstream.Statement, 0, len(records))
	for _, record := range records {
		if record.ID == "" || len(record.Payload) == 0 || record.UpdatedAt == "" || (record.Status != "active" && record.Status != "ended") {
			return fmt.Errorf("invalid local session")
		}
		statements = append(statements, upstream.Statement{SQL: `INSERT INTO local_sessions(session_id,payload,updated_at,status) VALUES(?,?,?,?)`, Args: []any{record.ID, record.Payload, record.UpdatedAt, record.Status}, RequireRowsAffected: 1})
	}
	_, err := d.Local.Batch(ctx, statements)
	if err != nil {
		for _, record := range records {
			if existing, readErr := d.ReadLocalSession(ctx, record.ID); readErr == nil && existing.ID == record.ID {
				return ErrLocalSessionExists
			}
		}
		return err
	}
	return nil
}

// UpdateLocalSession applies an optimistic mutation through the store writer.
// The previous payload is part of the predicate, preventing lost updates.
func (d *Databases) UpdateLocalSession(ctx context.Context, id string, oldPayload, newPayload []byte, updatedAt, status string) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local session store is unavailable")
	}
	if id == "" || len(oldPayload) == 0 || len(newPayload) == 0 || updatedAt == "" || status != "active" && status != "ended" {
		return fmt.Errorf("invalid local session update")
	}
	result, err := d.Local.Exec(ctx, `UPDATE local_sessions SET payload=?,updated_at=?,status=? WHERE session_id=? AND payload=? AND status='active'`, newPayload, updatedAt, status, id, oldPayload)
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrLocalSessionChanged
	}
	return nil
}
