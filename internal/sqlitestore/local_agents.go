package sqlitestore

import (
	"context"
	"fmt"
	"os"

	upstream "github.com/rceman/go-sqlite-store/store"
)

// LocalAgent is the gateway-local projection of one portable Agent record.
// The projection is operational read authority; Hub synchronization replaces
// the complete project set atomically during bootstrap/replication.
type LocalAgent struct {
	ProjectID string
	AgentID   string
	Payload   []byte
	UpdatedAt string
}

func (d *Databases) ReadLocalAgent(ctx context.Context, projectID, agentID string) (LocalAgent, error) {
	if d == nil || d.Local == nil {
		return LocalAgent{}, fmt.Errorf("local store is unavailable")
	}
	rows, err := d.Local.Query(ctx, `SELECT project_id,agent_id,payload,updated_at FROM local_agents WHERE project_id=? AND agent_id=?`, projectID, agentID)
	if err != nil {
		return LocalAgent{}, err
	}
	if len(rows.Rows) == 0 {
		return LocalAgent{}, fmt.Errorf("local agent %q/%q: %w", projectID, agentID, os.ErrNotExist)
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 4 {
		return LocalAgent{}, fmt.Errorf("invalid local agent row")
	}
	project, projectOK := rows.Rows[0][0].(string)
	agent, agentOK := rows.Rows[0][1].(string)
	payload, payloadOK := rows.Rows[0][2].([]byte)
	updatedAt, updatedOK := rows.Rows[0][3].(string)
	if !projectOK || !agentOK || !payloadOK || !updatedOK {
		return LocalAgent{}, fmt.Errorf("invalid local agent row")
	}
	return LocalAgent{ProjectID: project, AgentID: agent, Payload: append([]byte(nil), payload...), UpdatedAt: updatedAt}, nil
}

func (d *Databases) ListLocalAgents(ctx context.Context, projectID string, limit int) ([]LocalAgent, error) {
	if d == nil || d.Local == nil {
		return nil, fmt.Errorf("local store is unavailable")
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid local agent limit")
	}
	rows, err := d.Local.Query(ctx, `SELECT project_id,agent_id,payload,updated_at FROM local_agents WHERE project_id=? ORDER BY agent_id LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]LocalAgent, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid local agent row")
		}
		project, projectOK := row[0].(string)
		agent, agentOK := row[1].(string)
		payload, payloadOK := row[2].([]byte)
		updatedAt, updatedOK := row[3].(string)
		if !projectOK || !agentOK || !payloadOK || !updatedOK {
			return nil, fmt.Errorf("invalid local agent row")
		}
		result = append(result, LocalAgent{ProjectID: project, AgentID: agent, Payload: append([]byte(nil), payload...), UpdatedAt: updatedAt})
	}
	return result, nil
}

// ReplaceLocalAgents makes one complete project projection visible at once.
// It is deliberately Local-only and never creates Hub outbox work.
func (d *Databases) ReplaceLocalAgents(ctx context.Context, projectID string, agents []LocalAgent) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	if len(agents) > 1000 {
		return fmt.Errorf("local agent projection exceeds configured maximum")
	}
	statements := make([]upstream.Statement, 0, len(agents)+1)
	statements = append(statements, upstream.Statement{SQL: `DELETE FROM local_agents WHERE project_id=?`, Args: []any{projectID}})
	for _, agent := range agents {
		if agent.ProjectID != projectID || agent.AgentID == "" || len(agent.Payload) == 0 || agent.UpdatedAt == "" {
			return fmt.Errorf("invalid local agent projection")
		}
		statements = append(statements, upstream.Statement{
			SQL:  `INSERT INTO local_agents(project_id,agent_id,payload,updated_at) VALUES(?,?,?,?)`,
			Args: []any{agent.ProjectID, agent.AgentID, agent.Payload, agent.UpdatedAt},
		})
	}
	_, err := d.Local.Batch(ctx, statements)
	return err
}

// UpsertLocalAgent updates one locally authoritative Agent projection. Hub
// mutations may refresh this row after their admin transaction commits; live
// reads never need to consult that transaction's repository.
func (d *Databases) UpsertLocalAgent(ctx context.Context, agent LocalAgent) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	if agent.ProjectID == "" || agent.AgentID == "" || len(agent.Payload) == 0 || agent.UpdatedAt == "" {
		return fmt.Errorf("invalid local agent projection")
	}
	_, err := d.Local.Exec(ctx, `INSERT INTO local_agents(project_id,agent_id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(project_id,agent_id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, agent.ProjectID, agent.AgentID, agent.Payload, agent.UpdatedAt)
	return err
}
