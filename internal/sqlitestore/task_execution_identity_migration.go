package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (d *Databases) MigrateTaskExecutionAgentIdentity(ctx context.Context, projectID, legacyAgentID, canonicalAgentID string) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	if projectID == "" || legacyAgentID == "" || canonicalAgentID == "" || legacyAgentID == canonicalAgentID {
		return fmt.Errorf("invalid Task execution Agent identity migration")
	}
	rows, err := d.Local.Query(ctx, `SELECT task_id,status,agent FROM local_task_execution_states WHERE project_id=? AND agent=? AND status NOT IN (?,?) ORDER BY task_id`, projectID, legacyAgentID, model.TaskExecutionDone, model.TaskExecutionFailed)
	if err != nil {
		return err
	}
	statements := make([]upstream.Statement, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 3 {
			return fmt.Errorf("invalid Task execution Agent migration row")
		}
		taskID, taskOK := row[0].(string)
		status, statusOK := row[1].(string)
		agent, agentOK := row[2].(string)
		if !taskOK || !statusOK || !agentOK || taskID == "" || status == "" || agent != legacyAgentID {
			return fmt.Errorf("invalid Task execution Agent migration identity")
		}
		statements = append(statements, upstream.Statement{
			SQL:  `UPDATE local_task_execution_states SET agent=? WHERE project_id=? AND task_id=? AND agent=? AND status NOT IN (?,?)`,
			Args: []any{canonicalAgentID, projectID, taskID, legacyAgentID, model.TaskExecutionDone, model.TaskExecutionFailed}, RequireRowsAffected: 1,
		})
	}
	if len(statements) == 0 {
		return nil
	}
	_, err = d.Local.Batch(ctx, statements)
	return err
}

func (d *Databases) MigrateLocalAgentIdentity(ctx context.Context, projectID, legacyAgentID, canonicalAgentID string) error {
	if d == nil || d.Local == nil {
		return fmt.Errorf("local store is unavailable")
	}
	if projectID == "" || legacyAgentID == "" || canonicalAgentID == "" || legacyAgentID == canonicalAgentID {
		return fmt.Errorf("invalid Local Agent identity migration")
	}
	legacy, err := d.ReadLocalAgent(ctx, projectID, legacyAgentID)
	if err != nil {
		if isNotFoundError(err) {
			return nil
		}
		return err
	}
	if _, err := d.ReadLocalAgent(ctx, projectID, canonicalAgentID); err == nil {
		return fmt.Errorf("Local Agent identity migration collision for %q/%q", projectID, canonicalAgentID)
	} else if !isNotFoundError(err) {
		return err
	}
	var agent model.Agent
	if err := json.Unmarshal(legacy.Payload, &agent); err != nil {
		return fmt.Errorf("decode legacy Local Agent projection: %w", err)
	}
	if agent.ProjectID != projectID || agent.AgentID != legacyAgentID {
		return fmt.Errorf("legacy Local Agent projection identity mismatch")
	}
	agent.AgentID = canonicalAgentID
	if err := model.ValidateAgent(agent); err != nil {
		return fmt.Errorf("validate migrated Local Agent projection: %w", err)
	}
	payload, err := json.Marshal(agent)
	if err != nil {
		return fmt.Errorf("encode migrated Local Agent projection: %w", err)
	}
	_, err = d.Local.Batch(ctx, []upstream.Statement{
		{SQL: `INSERT INTO local_agents(project_id,agent_id,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{projectID, canonicalAgentID, payload, legacy.UpdatedAt}, RequireRowsAffected: 1},
		{SQL: `DELETE FROM local_agents WHERE project_id=? AND agent_id=?`, Args: []any{projectID, legacyAgentID}, RequireRowsAffected: 1},
	})
	return err
}

func isNotFoundError(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
