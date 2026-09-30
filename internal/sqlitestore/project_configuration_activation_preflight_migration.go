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

const projectConfigurationActivationPreflightMigrationID = "project_configuration_activation_preflight"

// gtwActivationPreflightProcedure is the canonical GTW disposable activation
// preflight Procedure definition. It is invoked directly through
// procedure/activation_preflight and intentionally has no Hook binding.
func gtwActivationPreflightProcedure() (model.ProjectProcedureDefinition, error) {
	output, err := model.ActivationPreflightProcedureOutputSchema(model.ActivationPreflightProcedureChecks)
	if err != nil {
		return model.ProjectProcedureDefinition{}, err
	}
	return model.ProjectProcedureDefinition{
		Script:  "scripts/activation-preflight.py",
		Summary: "Prove the exact source boots against a disposable snapshot of live durable state.",
		Guide:   "Binds the exact source commit/tree, snapshots Shared/Local databases consistently, boots the built candidate against the snapshot with an isolated Hub, applies migrations to readiness, reopens to prove migration idempotency, runs the bounded sessionless E2E, and returns compact structured evidence. Never mutates live state and never restarts the live Gateway.",
		Input:   model.ActivationPreflightProcedureInputSchema(),
		Output:  output,
	}, nil
}

// MigrateGTWActivationPreflightProcedure installs the canonical
// activation_preflight Procedure into the GTW self-host ProjectConfiguration.
// It runs once under a Shared upgrade marker and commits a normal
// project-configuration lifecycle revision so Hub converges through the
// regular Shared outbox path.
func (d *Databases) MigrateGTWActivationPreflightProcedure(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for ProjectConfiguration activation_preflight migration")
	}
	state, err := d.projectConfigurationMigrationState(ctx, projectConfigurationActivationPreflightMigrationID)
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
		// Hosts without the GTW self-host project have nothing to install;
		// leave the marker unset so a later onboarded or restored
		// gpt-tunnel-gateway configuration still receives the Procedure.
		return nil
	}
	if err := d.setSharedUpgradeMigrationState(ctx, projectConfigurationActivationPreflightMigrationID, "in_progress"); err != nil {
		return err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 2 {
		return fmt.Errorf("invalid Shared ProjectConfiguration row for activation_preflight migration")
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration revision for activation_preflight migration")
	}
	payload, ok := rows.Rows[0][1].([]byte)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration payload for activation_preflight migration")
	}
	configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
	if err != nil {
		return err
	}
	if int64(configuration.Revision) != revision {
		return fmt.Errorf("Shared ProjectConfiguration payload revision does not match row revision")
	}
	definition, err := gtwActivationPreflightProcedure()
	if err != nil {
		return err
	}
	if existing, exists := configuration.Procedures[model.ActivationPreflightProcedureName]; exists {
		current, marshalErr := json.Marshal(existing)
		wanted, wantedErr := json.Marshal(definition)
		if marshalErr != nil || wantedErr != nil {
			return fmt.Errorf("canonical activation_preflight Procedure could not be encoded")
		}
		if string(current) != string(wanted) {
			return fmt.Errorf("existing activation_preflight Procedure conflicts with the canonical migration")
		}
		return d.setSharedUpgradeMigrationState(ctx, projectConfigurationActivationPreflightMigrationID, "complete")
	}
	// The new revision must stay ahead of every recorded revision for this
	// configuration: interrupted publishes can leave Shared history and
	// pending outbox entries ahead of the row revision, and reusing such a
	// revision would collide in history or publish a divergent duplicate.
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
	candidate.Procedures[model.ActivationPreflightProcedureName] = definition
	candidate.Revision = int(nextRevision) + 1
	candidate.UpdatedBy = "gateway"
	candidate.UpdatedAt = time.Now().UTC()
	candidatePayload, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(candidatePayload)
	operationID := "project-configuration-activation-preflight-" + hex.EncodeToString(digest[:])
	changedFields, err := json.Marshal([]string{"procedures.activation_preflight"})
	if err != nil {
		return err
	}
	recorded := candidate.UpdatedAt.Format(time.RFC3339Nano)
	// A startup marker migration cannot use CommitSharedLifecycleRevision: it
	// requires Revision == ExpectedRevision+1, which is unreachable when
	// durable history or the pending outbox is already ahead of the row
	// revision. The batch below commits the same canonical triple atomically
	// — current state, Shared history, and Hub outbox — with a CAS on the
	// row revision so a concurrent mutation fails closed and the marker
	// retries on the next run.
	if _, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_project_configurations SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{candidate.Revision, candidatePayload, recorded, "gpt-tunnel-gateway", revision}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "update", "gateway", "Install the canonical activation_preflight Procedure for the GTW self-host project.", changedFields, candidatePayload, recorded}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{operationID, "project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "project-configuration-update", candidatePayload, recorded}, RequireRowsAffected: 1},
	}); err != nil {
		return err
	}
	return d.setSharedUpgradeMigrationState(ctx, projectConfigurationActivationPreflightMigrationID, "complete")
}
