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

const projectConfigurationProcedureSchemasMigrationID = "project_configuration_procedure_schemas_v1"

// gtwDeliveryProcedureDefinitions returns the canonical definitions of the
// three GTW delivery Procedures keyed by name.
func gtwDeliveryProcedureDefinitions() (map[string]model.ProjectProcedureDefinition, error) {
	preflight, err := gtwActivationPreflightProcedure()
	if err != nil {
		return nil, err
	}
	activate, err := gtwActivateLocalProcedure()
	if err != nil {
		return nil, err
	}
	release, err := gtwReleaseProdProcedure()
	if err != nil {
		return nil, err
	}
	return map[string]model.ProjectProcedureDefinition{
		model.ActivationPreflightProcedureName: preflight,
		model.ActivateLocalProcedureName:       activate,
		model.ReleaseProdProcedureName:         release,
	}, nil
}

// MigrateGTWProcedureSchemas converges already-installed GTW delivery
// Procedure definitions to their canonical schemas. It exists because the
// per-procedure install migrations are one-shot: hosts whose markers are
// already complete never re-run them, so schema corrections landed after the
// first install (for example the TSK686 GitFingerprint/EntityKeyAndReference
// refs) would stay unreachable. This migration runs under its own marker and
// rewrites any drifted same-identity definition — identical Script, Summary,
// and Guide — through one normal configuration revision. A foreign Procedure
// under a canonical name fails closed.
func (d *Databases) MigrateGTWProcedureSchemas(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for ProjectConfiguration procedure schema migration")
	}
	state, err := d.projectConfigurationMigrationState(ctx, projectConfigurationProcedureSchemasMigrationID)
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
	if err := d.setSharedUpgradeMigrationState(ctx, projectConfigurationProcedureSchemasMigrationID, "in_progress"); err != nil {
		return err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 2 {
		return fmt.Errorf("invalid Shared ProjectConfiguration row for procedure schema migration")
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration revision for procedure schema migration")
	}
	payload, ok := rows.Rows[0][1].([]byte)
	if !ok {
		return fmt.Errorf("invalid Shared ProjectConfiguration payload for procedure schema migration")
	}
	configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
	if err != nil {
		return err
	}
	if int64(configuration.Revision) != revision {
		return fmt.Errorf("Shared ProjectConfiguration payload revision does not match row revision")
	}
	canonical, err := gtwDeliveryProcedureDefinitions()
	if err != nil {
		return err
	}
	var drifted []string
	for _, name := range []string{model.ActivationPreflightProcedureName, model.ActivateLocalProcedureName, model.ReleaseProdProcedureName} {
		existing, exists := configuration.Procedures[name]
		if !exists {
			continue
		}
		current, marshalErr := json.Marshal(existing)
		wanted, wantedErr := json.Marshal(canonical[name])
		if marshalErr != nil || wantedErr != nil {
			return fmt.Errorf("canonical %s Procedure could not be encoded", name)
		}
		if string(current) == string(wanted) {
			continue
		}
		if existing.Script != canonical[name].Script || existing.Summary != canonical[name].Summary || existing.Guide != canonical[name].Guide {
			return fmt.Errorf("existing %s Procedure conflicts with the canonical migration", name)
		}
		drifted = append(drifted, name)
	}
	if len(drifted) == 0 {
		return d.setSharedUpgradeMigrationState(ctx, projectConfigurationProcedureSchemasMigrationID, "complete")
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
	candidate.Procedures = make(map[string]model.ProjectProcedureDefinition, len(configuration.Procedures))
	for name, value := range configuration.Procedures {
		candidate.Procedures[name] = value
	}
	for _, name := range drifted {
		candidate.Procedures[name] = canonical[name]
	}
	candidate.Revision = int(nextRevision) + 1
	candidate.UpdatedBy = "gateway"
	candidate.UpdatedAt = time.Now().UTC()
	candidatePayload, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(candidatePayload)
	operationID := "project-configuration-procedure-schemas-" + hex.EncodeToString(digest[:])
	changed := make([]string, len(drifted))
	for index, name := range drifted {
		changed[index] = "procedures." + name
	}
	changedFields, err := json.Marshal(changed)
	if err != nil {
		return err
	}
	recorded := candidate.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := d.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `UPDATE shared_project_configurations SET revision=?,payload=?,updated_at=? WHERE id=? AND revision=?`, Args: []any{candidate.Revision, candidatePayload, recorded, "gpt-tunnel-gateway", revision}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{"project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "update", "gateway", "Reconcile installed GTW delivery Procedure schemas to canonical shape.", changedFields, candidatePayload, recorded}, RequireRowsAffected: 1},
		{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, Args: []any{operationID, "project_configuration", "gpt-tunnel-gateway", "gpt-tunnel-gateway", candidate.Revision, "project-configuration-update", candidatePayload, recorded}, RequireRowsAffected: 1},
	}); err != nil {
		return err
	}
	return d.setSharedUpgradeMigrationState(ctx, projectConfigurationProcedureSchemasMigrationID, "complete")
}
