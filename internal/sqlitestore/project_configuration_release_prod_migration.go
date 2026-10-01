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

const projectConfigurationReleaseProdMigrationID = "project_configuration_release_prod"

// gtwReleaseProdProcedure is the canonical GTW production-release Procedure
// definition. It is invoked directly through procedure/release_prod and
// intentionally has no Hook binding.
func gtwReleaseProdProcedure() (model.ProjectProcedureDefinition, error) {
	output, err := model.ReleaseProdProcedureOutputSchema(model.ReleaseProdProcedureChecks)
	if err != nil {
		return model.ProjectProcedureDefinition{}, err
	}
	return model.ProjectProcedureDefinition{
		Script:  "scripts/release-prod.py",
		Summary: "Release the exact source an accepted Track authorizes through the canonical tag/publish path.",
		Guide:   "Requires the accepted Track plus exact source commit/tree authority; validates that durable authority and the clean checkout before any external side effect, creates and verifies the annotated tag through the canonical release tooling, pushes it to the configured remote, and proves publication through the typed release verification with structured provenance. Canonical source advance after acceptance requires a new accept cycle before another release.",
		Input:   model.ReleaseProdProcedureInputSchema(),
		Output:  output,
	}, nil
}

// MigrateGTWReleaseProdProcedure installs the canonical release_prod
// Procedure into the GTW self-host ProjectConfiguration. It runs once under a
// Shared upgrade marker and commits a normal project-configuration lifecycle
// revision so Hub converges through the regular Shared outbox path.
func (d *Databases) MigrateGTWReleaseProdProcedure(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for ProjectConfiguration release_prod migration")
	}
	state, err := d.projectConfigurationMigrationState(ctx, projectConfigurationReleaseProdMigrationID)
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
	if err := d.setSharedUpgradeMigrationState(ctx, projectConfigurationReleaseProdMigrationID, "in_progress"); err != nil {
		return err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 2 {
		return fmt.Errorf("invalid Shared ProjectConfiguration row for release_prod migration")
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration revision for release_prod migration")
	}
	payload, ok := rows.Rows[0][1].([]byte)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration payload for release_prod migration")
	}
	configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
	if err != nil {
		return err
	}
	if int64(configuration.Revision) != revision {
		return fmt.Errorf("Shared ProjectConfiguration payload revision does not match row revision")
	}
	definition, err := gtwReleaseProdProcedure()
	if err != nil {
		return err
	}
	if existing, exists := configuration.Procedures[model.ReleaseProdProcedureName]; exists {
		current, marshalErr := json.Marshal(existing)
		wanted, wantedErr := json.Marshal(definition)
		if marshalErr != nil || wantedErr != nil {
			return fmt.Errorf("canonical release_prod Procedure could not be encoded")
		}
		if string(current) != string(wanted) {
			return fmt.Errorf("existing release_prod Procedure conflicts with the canonical migration")
		}
		return d.setSharedUpgradeMigrationState(ctx, projectConfigurationReleaseProdMigrationID, "complete")
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
	candidate.Procedures[model.ReleaseProdProcedureName] = definition
	candidate.Revision = int(nextRevision) + 1
	candidate.UpdatedBy = "gateway"
	candidate.UpdatedAt = time.Now().UTC()
	candidatePayload, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(candidatePayload)
	operationID := "project-configuration-release-prod-" + hex.EncodeToString(digest[:])
	changedFields, err := json.Marshal([]string{"procedures.release_prod"})
	if err != nil {
		return err
	}
	recorded := candidate.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_project_configurations SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{candidate.Revision, candidatePayload, recorded, "gpt-tunnel-gateway", revision}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "update", "gateway", "Install the canonical release_prod Procedure for the GTW self-host project.", changedFields, candidatePayload, recorded}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{operationID, "project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "project-configuration-update", candidatePayload, recorded}, RequireRowsAffected: 1},
	}); err != nil {
		return err
	}
	return d.setSharedUpgradeMigrationState(ctx, projectConfigurationReleaseProdMigrationID, "complete")
}
