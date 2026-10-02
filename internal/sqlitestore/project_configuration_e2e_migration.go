package sqlitestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const projectConfigurationE2EMigrationID = "project_configuration_e2e_procedure_v1"

// MigrateGTWE2EProcedure installs the canonical e2e Procedure into the GTW
// self-host ProjectConfiguration (TSK693, JRN25). It runs once under a Shared
// upgrade marker and commits a normal project-configuration lifecycle
// revision so Hub converges through the regular Shared outbox path.
func (d *Databases) MigrateGTWE2EProcedure(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for ProjectConfiguration e2e migration")
	}
	state, err := d.projectConfigurationMigrationState(ctx, projectConfigurationE2EMigrationID)
	if err != nil {
		return err
	}
	if state == "complete" {
		return nil
	}
	rows, err := d.Shared.Query(ctx, `SELECT revision,payload FROM shared_project_configurations WHERE id=?`, "gpt-tunnel-gateway")
	if err != nil {
		return err
	}
	if len(rows.Rows) == 0 {
		return nil
	}
	if err := d.setSharedUpgradeMigrationState(ctx, projectConfigurationE2EMigrationID, "in_progress"); err != nil {
		return err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 2 {
		return fmt.Errorf("invalid Shared ProjectConfiguration row for e2e migration")
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration revision for e2e migration")
	}
	payload, ok := rows.Rows[0][1].([]byte)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration payload for e2e migration")
	}
	configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
	if err != nil {
		return err
	}
	if int64(configuration.Revision) != revision {
		return fmt.Errorf("Shared ProjectConfiguration payload revision does not match row revision")
	}
	definition, err := model.GTWE2EProcedureDefinition()
	if err != nil {
		return err
	}
	if existing, exists := configuration.Procedures[model.GTWE2EProcedureName]; exists {
		current, marshalErr := json.Marshal(existing)
		wanted, wantedErr := json.Marshal(definition)
		if marshalErr != nil || wantedErr != nil {
			return fmt.Errorf("canonical e2e Procedure could not be encoded")
		}
		if string(current) == string(wanted) {
			return d.setSharedUpgradeMigrationState(ctx, projectConfigurationE2EMigrationID, "complete")
		}
		if existing.Script != definition.Script || existing.Summary != definition.Summary || existing.Guide != definition.Guide {
			return fmt.Errorf("existing e2e Procedure conflicts with the canonical migration")
		}
	}
	nextRevision := revision
	for _, probe := range []string{
		`SELECT MAX(revision) FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id='gpt-tunnel-gateway'`,
		`SELECT MAX(revision) FROM hub_outbox WHERE entity_type='project_configuration' AND entity_id='gpt-tunnel-gateway'`,
	} {
		probeRows, err := d.Shared.Query(ctx, probe)
		if err != nil {
			return err
		}
		if len(probeRows.Rows) == 1 && len(probeRows.Rows[0]) == 1 {
			if maximum, isInt := probeRows.Rows[0][0].(int64); isInt && maximum > nextRevision {
				nextRevision = maximum
			}
		}
	}
	candidate := configuration
	candidate.Procedures = make(map[string]model.ProjectProcedureDefinition, len(configuration.Procedures)+1)
	for name, value := range configuration.Procedures {
		candidate.Procedures[name] = value
	}
	candidate.Procedures[model.GTWE2EProcedureName] = definition
	candidate.Revision = int(nextRevision) + 1
	candidate.UpdatedBy = "gateway"
	candidate.UpdatedAt = time.Now().UTC()
	candidatePayload, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(candidatePayload)
	operationID := "project-configuration-e2e-" + hex.EncodeToString(digest[:])
	changedFields, err := json.Marshal([]string{"procedures.e2e"})
	if err != nil {
		return err
	}
	recorded := candidate.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_project_configurations SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{candidate.Revision, candidatePayload, recorded, "gpt-tunnel-gateway", revision}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "update", "gateway", "Install the canonical e2e Procedure for the GTW self-host project.", changedFields, candidatePayload, recorded}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{operationID, "project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "project-configuration-update", candidatePayload, recorded}, RequireRowsAffected: 1},
	}); err != nil {
		return err
	}
	return d.setSharedUpgradeMigrationState(ctx, projectConfigurationE2EMigrationID, "complete")
}
