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

const projectConfigurationActivateLocalMigrationID = "project_configuration_activate_local"

// gtwActivateLocalProcedure is the canonical GTW live-activation Procedure
// definition. It is invoked directly through procedure/activate_local and
// intentionally has no Hook binding.
func gtwActivateLocalProcedure() (model.ProjectProcedureDefinition, error) {
	output, err := model.ActivateLocalProcedureOutputSchema(model.ActivateLocalProcedureChecks)
	if err != nil {
		return model.ProjectProcedureDefinition{}, err
	}
	return model.ProjectProcedureDefinition{
		Script:  "scripts/activate-local.py",
		Summary: "Activate the exact source an accepted Track authorizes onto the live Gateway.",
		Guide:   "Requires the accepted Track plus exact source commit/tree authority; validates that durable authority and the clean checkout before any live mutation, runs the disposable exact-source preflight, performs the binary cutover with the bounded artifact+durable-state recovery boundary, proves readiness and exact running-source identity, and preserves the Tunnel process. Failed activations keep their evidence and permit bounded truthful retry.",
		Input:   model.ActivateLocalProcedureInputSchema(),
		Output:  output,
	}, nil
}

// MigrateGTWActivateLocalProcedure installs the canonical activate_local
// Procedure into the GTW self-host ProjectConfiguration. It runs once under a
// Shared upgrade marker and commits a normal project-configuration lifecycle
// revision so Hub converges through the regular Shared outbox path.
func (d *Databases) MigrateGTWActivateLocalProcedure(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for ProjectConfiguration activate_local migration")
	}
	state, err := d.projectConfigurationMigrationState(ctx, projectConfigurationActivateLocalMigrationID)
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
	if err := d.setSharedUpgradeMigrationState(ctx, projectConfigurationActivateLocalMigrationID, "in_progress"); err != nil {
		return err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 2 {
		return fmt.Errorf("invalid Shared ProjectConfiguration row for activate_local migration")
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration revision for activate_local migration")
	}
	payload, ok := rows.Rows[0][1].([]byte)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration payload for activate_local migration")
	}
	configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
	if err != nil {
		return err
	}
	if int64(configuration.Revision) != revision {
		return fmt.Errorf("Shared ProjectConfiguration payload revision does not match row revision")
	}
	definition, err := gtwActivateLocalProcedure()
	if err != nil {
		return err
	}
	if existing, exists := configuration.Procedures[model.ActivateLocalProcedureName]; exists {
		current, marshalErr := json.Marshal(existing)
		wanted, wantedErr := json.Marshal(definition)
		if marshalErr != nil || wantedErr != nil {
			return fmt.Errorf("canonical activate_local Procedure could not be encoded")
		}
		if string(current) == string(wanted) {
			return d.setSharedUpgradeMigrationState(ctx, projectConfigurationActivateLocalMigrationID, "complete")
		}
		// The known predecessor differs from the canonical definition only
		// in schema shape (the pre-TSK686 inline fingerprint fields): when
		// Script/Summary/Guide still identify this Procedure, reconcile the
		// drifted schemas through a normal configuration revision. A foreign
		// Procedure under the same name still fails closed.
		if existing.Script != definition.Script || existing.Summary != definition.Summary || existing.Guide != definition.Guide {
			return fmt.Errorf("existing activate_local Procedure conflicts with the canonical migration")
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
	candidate.Procedures[model.ActivateLocalProcedureName] = definition
	candidate.Revision = int(nextRevision) + 1
	candidate.UpdatedBy = "gateway"
	candidate.UpdatedAt = time.Now().UTC()
	candidatePayload, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(candidatePayload)
	operationID := "project-configuration-activate-local-" + hex.EncodeToString(digest[:])
	changedFields, err := json.Marshal([]string{"procedures.activate_local"})
	if err != nil {
		return err
	}
	recorded := candidate.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_project_configurations SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{candidate.Revision, candidatePayload, recorded, "gpt-tunnel-gateway", revision}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "update", "gateway", "Install the canonical activate_local Procedure for the GTW self-host project.", changedFields, candidatePayload, recorded}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{operationID, "project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "project-configuration-update", candidatePayload, recorded}, RequireRowsAffected: 1},
	}); err != nil {
		return err
	}
	return d.setSharedUpgradeMigrationState(ctx, projectConfigurationActivateLocalMigrationID, "complete")
}
