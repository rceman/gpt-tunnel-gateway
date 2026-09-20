package sqlitestore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrSessionBootstrapGrantNotFound = errors.New("session bootstrap grant not found")
)

type SessionBootstrapGrant struct {
	ProjectID   string
	ProjectCode string
	GatewayID   string
	Role        string
	AgentID     string
	Token       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (d *Databases) EnsureSessionBootstrapGrant(ctx context.Context, input SessionBootstrapGrant) (SessionBootstrapGrant, error) {
	if d == nil || d.Local == nil {
		return SessionBootstrapGrant{}, fmt.Errorf("local session bootstrap grant store is unavailable")
	}
	if err := validateSessionBootstrapGrantMetadata(input); err != nil {
		return SessionBootstrapGrant{}, err
	}
	if input.Token != "" {
		if err := validateSessionBootstrapToken(input.Token); err != nil {
			return SessionBootstrapGrant{}, err
		}
	}
	existing, err := d.ReadSessionBootstrapGrant(ctx, input.ProjectID)
	if err == nil {
		if existing.ProjectCode != input.ProjectCode || existing.GatewayID != input.GatewayID || existing.Role != input.Role || existing.AgentID != input.AgentID || existing.Token == "" {
			return SessionBootstrapGrant{}, fmt.Errorf("session bootstrap grant conflicts with project authority")
		}
		return existing, nil
	}
	if !errors.Is(err, ErrSessionBootstrapGrantNotFound) {
		return SessionBootstrapGrant{}, err
	}
	if input.Token == "" {
		input.Token, err = newSessionBootstrapToken()
		if err != nil {
			return SessionBootstrapGrant{}, err
		}
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	if input.UpdatedAt.IsZero() {
		input.UpdatedAt = input.CreatedAt
	}
	_, err = d.Local.Exec(ctx, `INSERT INTO local_session_bootstrap_grants(project_id,project_code,gateway_id,role,agent_id,token,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(project_id) DO NOTHING`, input.ProjectID, input.ProjectCode, input.GatewayID, input.Role, input.AgentID, input.Token, formatBootstrapGrantTime(input.CreatedAt), formatBootstrapGrantTime(input.UpdatedAt))
	if err != nil {
		return SessionBootstrapGrant{}, err
	}
	stored, err := d.ReadSessionBootstrapGrant(ctx, input.ProjectID)
	if err != nil {
		return SessionBootstrapGrant{}, err
	}
	if stored.ProjectCode != input.ProjectCode || stored.GatewayID != input.GatewayID || stored.Role != input.Role || stored.AgentID != input.AgentID {
		return SessionBootstrapGrant{}, fmt.Errorf("session bootstrap grant conflicts with project authority")
	}
	return stored, nil
}

func (d *Databases) ReadSessionBootstrapGrant(ctx context.Context, projectID string) (SessionBootstrapGrant, error) {
	if d == nil || d.Local == nil {
		return SessionBootstrapGrant{}, fmt.Errorf("local session bootstrap grant store is unavailable")
	}
	if strings.TrimSpace(projectID) == "" {
		return SessionBootstrapGrant{}, fmt.Errorf("session bootstrap grant project is required")
	}
	rows, err := d.Local.Query(ctx, `SELECT project_id,project_code,gateway_id,role,agent_id,token,created_at,updated_at FROM local_session_bootstrap_grants WHERE project_id=?`, projectID)
	if err != nil {
		return SessionBootstrapGrant{}, err
	}
	if len(rows.Rows) == 0 {
		return SessionBootstrapGrant{}, ErrSessionBootstrapGrantNotFound
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 8 {
		return SessionBootstrapGrant{}, fmt.Errorf("invalid session bootstrap grant row")
	}
	return decodeSessionBootstrapGrantRow(rows.Rows[0])
}

func (d *Databases) ReadSessionBootstrapGrantByToken(ctx context.Context, token string) (SessionBootstrapGrant, error) {
	if d == nil || d.Local == nil {
		return SessionBootstrapGrant{}, fmt.Errorf("local session bootstrap grant store is unavailable")
	}
	if err := validateSessionBootstrapToken(token); err != nil {
		return SessionBootstrapGrant{}, err
	}
	rows, err := d.Local.Query(ctx, `SELECT project_id,project_code,gateway_id,role,agent_id,token,created_at,updated_at FROM local_session_bootstrap_grants WHERE token=?`, token)
	if err != nil {
		return SessionBootstrapGrant{}, err
	}
	if len(rows.Rows) == 0 {
		return SessionBootstrapGrant{}, ErrSessionBootstrapGrantNotFound
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 8 {
		return SessionBootstrapGrant{}, fmt.Errorf("invalid session bootstrap grant row")
	}
	return decodeSessionBootstrapGrantRow(rows.Rows[0])
}

func decodeSessionBootstrapGrantRow(row []any) (SessionBootstrapGrant, error) {
	if len(row) != 8 {
		return SessionBootstrapGrant{}, fmt.Errorf("invalid session bootstrap grant row")
	}
	values := make([]string, len(row))
	for i := range values {
		value, ok := row[i].(string)
		if !ok {
			return SessionBootstrapGrant{}, fmt.Errorf("invalid session bootstrap grant row")
		}
		values[i] = value
	}
	created, err := time.Parse(time.RFC3339Nano, values[6])
	if err != nil {
		return SessionBootstrapGrant{}, fmt.Errorf("invalid session bootstrap grant created_at")
	}
	updated, err := time.Parse(time.RFC3339Nano, values[7])
	if err != nil {
		return SessionBootstrapGrant{}, fmt.Errorf("invalid session bootstrap grant updated_at")
	}
	grant := SessionBootstrapGrant{
		ProjectID:   values[0],
		ProjectCode: values[1],
		GatewayID:   values[2],
		Role:        values[3],
		AgentID:     values[4],
		Token:       values[5],
		CreatedAt:   created,
		UpdatedAt:   updated,
	}
	if err := validateSessionBootstrapGrantInput(grant); err != nil {
		return SessionBootstrapGrant{}, err
	}
	return grant, nil
}

func validateSessionBootstrapGrantMetadata(input SessionBootstrapGrant) error {
	for name, value := range map[string]string{"project_id": input.ProjectID, "project_code": input.ProjectCode, "gateway_id": input.GatewayID, "role": input.Role} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("session bootstrap grant %s is required", name)
		}
	}
	return nil
}

func validateSessionBootstrapGrantInput(input SessionBootstrapGrant) error {
	if err := validateSessionBootstrapGrantMetadata(input); err != nil {
		return err
	}
	return validateSessionBootstrapToken(input.Token)
}

func validateSessionBootstrapToken(token string) error {
	if len(token) < 24 || len(token) > 256 || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\t ") {
		return fmt.Errorf("invalid session bootstrap token")
	}
	return nil
}

func newSessionBootstrapToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate session bootstrap token: %w", err)
	}
	return "gtwbt_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func formatBootstrapGrantTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
