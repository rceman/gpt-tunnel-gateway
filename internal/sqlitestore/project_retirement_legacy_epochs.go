package sqlitestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const legacyCallbackEpochPredicate = `emitted_at IS NOT NULL AND emitted_at<>'' AND COALESCE(hook_outcome,'')='' AND COALESCE(operation_id,'')='' AND COALESCE(session_id,'')='' AND COALESCE(hook_completed_at,'')=''`

type ProjectRetirementLegacyEpochEvidence struct {
	Count  int
	SHA256 string
}

func (d *Databases) ReadProjectRetirementLegacyEpochEvidence(ctx context.Context, projectID string) (ProjectRetirementLegacyEpochEvidence, error) {
	evidence, _, err := d.readProjectRetirementLegacyEpochEvidence(ctx, projectID)
	return evidence, err
}

func (d *Databases) BeginDebugProjectRetirementWithLegacyEpochEvidence(ctx context.Context, retirement model.ProjectRetirement) error {
	evidence, epochIDs, err := d.readProjectRetirementLegacyEpochEvidence(ctx, retirement.ProjectID)
	if err != nil {
		return err
	}
	if retirement.LegacyCallbackEpochCount != evidence.Count || retirement.LegacyCallbackEpochSHA256 != evidence.SHA256 {
		return ErrProjectRetirementConflict
	}
	return d.beginLocalProjectRetirement(ctx, retirement, epochIDs, true)
}

func (d *Databases) readProjectRetirementLegacyEpochEvidence(ctx context.Context, projectID string) (ProjectRetirementLegacyEpochEvidence, []string, error) {
	if d == nil || d.Local == nil || model.ValidateProjectIdentifier(projectID) != nil {
		return ProjectRetirementLegacyEpochEvidence{}, nil, fmt.Errorf("invalid Local legacy callback epoch selector")
	}
	rows, err := d.Local.Query(ctx, `SELECT epoch_id,project_id,emitted_at,COALESCE(hook_outcome,''),COALESCE(operation_id,''),COALESCE(session_id,''),COALESCE(hook_completed_at,'') FROM local_callback_epochs WHERE project_id=? AND `+legacyCallbackEpochPredicate+` ORDER BY epoch_id LIMIT ?`, projectID, maxProjectRetirementRows+1)
	if err != nil {
		return ProjectRetirementLegacyEpochEvidence{}, nil, err
	}
	if len(rows.Rows) > maxProjectRetirementRows {
		return ProjectRetirementLegacyEpochEvidence{}, nil, fmt.Errorf("legacy callback epoch retirement evidence exceeds bounded row maximum")
	}
	epochIDs := make([]string, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 7 {
			return ProjectRetirementLegacyEpochEvidence{}, nil, fmt.Errorf("invalid legacy callback epoch row")
		}
		id, idOK := row[0].(string)
		rowProjectID, projectOK := row[1].(string)
		emittedAt, emittedOK := row[2].(string)
		outcome, outcomeOK := row[3].(string)
		operationID, operationOK := row[4].(string)
		sessionID, sessionOK := row[5].(string)
		hookCompletedAt, completedOK := row[6].(string)
		_, timeErr := time.Parse(time.RFC3339Nano, emittedAt)
		if !idOK || !projectOK || !emittedOK || !outcomeOK || !operationOK || !sessionOK || !completedOK || id == "" || rowProjectID != projectID || emittedAt == "" || timeErr != nil || outcome != "" || operationID != "" || sessionID != "" || hookCompletedAt != "" {
			return ProjectRetirementLegacyEpochEvidence{}, nil, fmt.Errorf("invalid legacy callback epoch values")
		}
		epochIDs = append(epochIDs, id)
	}
	if len(epochIDs) == 0 {
		return ProjectRetirementLegacyEpochEvidence{}, epochIDs, nil
	}
	return ProjectRetirementLegacyEpochEvidence{
		Count:  len(epochIDs),
		SHA256: projectRetirementLegacyEpochDigest(projectID, epochIDs),
	}, epochIDs, nil
}

func projectRetirementLegacyEpochDigest(projectID string, epochIDs []string) string {
	if len(epochIDs) == 0 {
		return ""
	}
	ordered := append([]string(nil), epochIDs...)
	sort.Strings(ordered)
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "GTW-project-retirement-legacy-epochs-v1:%d:%s\n", len(projectID), projectID)
	for _, id := range ordered {
		_, _ = fmt.Fprintf(hash, "%d:%s\n", len(id), id)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (d *Databases) localProjectRetirementBlockedAllowingLegacyEpochs(ctx context.Context, projectID string) (bool, error) {
	rows, err := d.Local.Query(ctx, `SELECT
EXISTS(SELECT 1 FROM local_sessions WHERE status='active' AND session_type<>'admin' AND (project_id=? OR project_id=''))
OR EXISTS(SELECT 1 FROM local_operations WHERE project_id=? AND status NOT IN ('completed','failed'))
OR EXISTS(SELECT 1 FROM local_task_execution_states WHERE project_id=? AND status NOT IN ('done','failed','abandoned'))
OR EXISTS(SELECT 1 FROM local_callback_epochs WHERE project_id=? AND NOT ((emitted_at IS NOT NULL AND COALESCE(hook_outcome,'') IN ('completed','failed','unbound')) OR (`+legacyCallbackEpochPredicate+`)))`, projectID, projectID, projectID, projectID)
	if err != nil {
		return false, err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return false, fmt.Errorf("invalid Local project retirement safety query")
	}
	blocked, ok := rows.Rows[0][0].(int64)
	if !ok || blocked != 0 && blocked != 1 {
		return false, fmt.Errorf("invalid Local project retirement safety result")
	}
	return blocked == 1, nil
}

func localProjectRetirementInsertStatement(retirement model.ProjectRetirement, allowLegacy bool, epochIDs []string) upstream.Statement {
	projectID := retirement.ProjectID
	sql := `INSERT INTO local_project_retirements(project_id,reason,retired_at)
SELECT ?,?,?
WHERE NOT EXISTS(SELECT 1 FROM local_sessions WHERE status='active' AND session_type<>'admin' AND (project_id=? OR project_id=''))
AND NOT EXISTS(SELECT 1 FROM local_operations WHERE project_id=? AND status NOT IN ('completed','failed'))
AND NOT EXISTS(SELECT 1 FROM local_task_execution_states WHERE project_id=? AND status NOT IN ('done','failed','abandoned'))`
	args := []any{projectID, retirement.Reason, retirement.RetiredAt.UTC().Format(time.RFC3339Nano), projectID, projectID, projectID}
	if !allowLegacy {
		sql += ` AND NOT EXISTS(SELECT 1 FROM local_callback_epochs WHERE project_id=? AND (emitted_at IS NULL OR hook_outcome IS NULL OR hook_outcome NOT IN ('completed','failed','unbound')))`
		args = append(args, projectID)
		return upstream.Statement{SQL: sql, Args: args, RequireRowsAffected: 1}
	}
	sql += ` AND NOT EXISTS(SELECT 1 FROM local_callback_epochs WHERE project_id=? AND NOT ((emitted_at IS NOT NULL AND COALESCE(hook_outcome,'') IN ('completed','failed','unbound')) OR (` + legacyCallbackEpochPredicate + `)))`
	args = append(args, projectID)
	sql += ` AND (SELECT COUNT(*) FROM local_callback_epochs WHERE project_id=? AND ` + legacyCallbackEpochPredicate + `)=?`
	args = append(args, projectID, len(epochIDs))
	if len(epochIDs) == 0 {
		sql += ` AND NOT EXISTS(SELECT 1 FROM local_callback_epochs WHERE project_id=? AND ` + legacyCallbackEpochPredicate + `)`
		args = append(args, projectID)
	} else {
		placeholders := strings.Repeat("?,", len(epochIDs))
		placeholders = placeholders[:len(placeholders)-1]
		sql += ` AND NOT EXISTS(SELECT 1 FROM local_callback_epochs WHERE project_id=? AND ` + legacyCallbackEpochPredicate + ` AND epoch_id NOT IN (` + placeholders + `))`
		args = append(args, projectID)
		for _, id := range epochIDs {
			args = append(args, id)
		}
	}
	return upstream.Statement{SQL: sql, Args: args, RequireRowsAffected: 1}
}
