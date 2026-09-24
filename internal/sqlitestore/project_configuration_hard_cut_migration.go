package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const projectConfigurationHardCutMigrationID = "project_configuration_v1_to_v2"
const projectConfigurationHardCutMaxRows = 4096
const projectConfigurationHardCutBatchSize = 128

var projectConfigurationV1Fields = map[string]struct{}{
	"schema_version": {}, "project_id": {}, "revision": {}, "execution_model": {}, "agent_routing": {},
	"workflow": {}, "checkpoint": {}, "integration": {}, "guide_bindings": {}, "callbacks": {},
	"activation_profile_ref": {}, "updated_by": {}, "updated_at": {},
}

func MigrateProjectConfigurationPayload(data []byte) (model.ProjectConfiguration, []byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("decode ProjectConfiguration migration payload")
	}
	for field := range fields {
		if _, ok := projectConfigurationV1Fields[field]; !ok {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("unknown ProjectConfiguration migration field %q", field)
		}
	}
	var version int
	if err := json.Unmarshal(fields["schema_version"], &version); err != nil {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("invalid ProjectConfiguration schema version")
	}
	switch version {
	case 1:
		if marker, present := fields["execution_model"]; present {
			var value string
			if err := json.Unmarshal(marker, &value); err != nil || value != "legacy" && value != "train_v2" {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("unsupported retired ProjectConfiguration execution model")
			}
			delete(fields, "execution_model")
		}
		fields["schema_version"], _ = json.Marshal(model.ProjectConfigurationSchemaVersion)
	case model.ProjectConfigurationSchemaVersion:
		if _, present := fields["execution_model"]; present {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("canonical ProjectConfiguration contains retired execution_model")
		}
	default:
		return model.ProjectConfiguration{}, nil, fmt.Errorf("unsupported ProjectConfiguration schema version %d", version)
	}
	canonicalInput, err := json.Marshal(fields)
	if err != nil {
		return model.ProjectConfiguration{}, nil, err
	}
	var configuration model.ProjectConfiguration
	decoder := json.NewDecoder(bytes.NewReader(canonicalInput))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("decode canonical ProjectConfiguration: %w", err)
	}
	if version == 1 {
		if configuration.GuideBindings == nil {
			configuration.GuideBindings = map[string]string{}
		}
		if configuration.Workflow.GateCommands.IsZero() {
			configuration.Workflow.GateCommands = model.DefaultProjectGateCommands()
		}
		if configuration.Integration.TargetBranch == "" {
			configuration.Integration.TargetBranch = configuration.Workflow.IntegrationBranch
		}
	}
	if configuration.SchemaVersion != model.ProjectConfigurationSchemaVersion || model.ValidateProjectIdentifier(configuration.ProjectID) != nil || configuration.Revision < 1 || configuration.UpdatedAt.IsZero() || configuration.UpdatedBy == "" || configuration.GuideBindings == nil || configuration.Workflow.GateCommands.IsZero() || configuration.Integration.TargetBranch == "" {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("ProjectConfiguration migration produced an incomplete canonical record")
	}
	canonical, err := json.Marshal(configuration)
	if err != nil {
		return model.ProjectConfiguration{}, nil, err
	}
	return configuration, canonical, nil
}

func DecodeCanonicalProjectConfigurationPayload(data []byte) (model.ProjectConfiguration, error) {
	var configuration model.ProjectConfiguration
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return model.ProjectConfiguration{}, fmt.Errorf("unexpected canonical ProjectConfiguration payload suffix")
	}
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectConfiguration{}, err
	}
	return configuration, nil
}

func (d *Databases) MigrateProjectConfigurationToCanonical(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for ProjectConfiguration migration")
	}
	state, err := d.sharedUpgradeMigrationState(ctx, projectConfigurationHardCutMigrationID)
	if err != nil {
		return err
	}
	if state == "complete" {
		return nil
	}
	statements := make([]upstream.Statement, 0, 3*projectConfigurationHardCutMaxRows+1)
	rows, err := d.Shared.Query(ctx, `SELECT id,revision,payload FROM shared_project_configurations ORDER BY id LIMIT ?`, int64(projectConfigurationHardCutMaxRows+1))
	if err != nil {
		return err
	}
	if len(rows.Rows) > projectConfigurationHardCutMaxRows {
		return fmt.Errorf("ProjectConfiguration migration exceeds bounded row maximum")
	}
	for _, row := range rows.Rows {
		if len(row) != 3 {
			return fmt.Errorf("invalid Shared ProjectConfiguration row")
		}
		id, idOK := row[0].(string)
		revision, revisionOK := row[1].(int64)
		payload, payloadOK := row[2].([]byte)
		if !payloadOK {
			if text, ok := row[2].(string); ok {
				payload, payloadOK = []byte(text), true
			}
		}
		if !idOK || !revisionOK || !payloadOK {
			return fmt.Errorf("invalid Shared ProjectConfiguration row types")
		}
		configuration, canonical, err := MigrateProjectConfigurationPayload(payload)
		if err != nil {
			return fmt.Errorf("migrate Shared ProjectConfiguration %q: %w", id, err)
		}
		if configuration.ProjectID != id || int64(configuration.Revision) != revision {
			return fmt.Errorf("Shared ProjectConfiguration %q conflicts with its row identity", id)
		}
		statements = append(statements, upstream.Statement{SQL: `UPDATE shared_project_configurations SET payload=? WHERE id=? AND revision=?`, Args: []any{canonical, id, revision}})
	}
	outbox, err := d.Shared.Query(ctx, `SELECT id,entity_id,revision,payload FROM hub_outbox WHERE entity_type='project_configuration' ORDER BY id LIMIT ?`, int64(projectConfigurationHardCutMaxRows+1))
	if err != nil {
		return err
	}
	if len(outbox.Rows) > projectConfigurationHardCutMaxRows {
		return fmt.Errorf("ProjectConfiguration outbox migration exceeds bounded row maximum")
	}
	for _, row := range outbox.Rows {
		if len(row) != 4 {
			return fmt.Errorf("invalid ProjectConfiguration outbox row")
		}
		id, idOK := row[0].(string)
		entityID, entityOK := row[1].(string)
		revision, revisionOK := row[2].(int64)
		payload, payloadOK := row[3].([]byte)
		if !payloadOK {
			if text, ok := row[3].(string); ok {
				payload, payloadOK = []byte(text), true
			}
		}
		if !idOK || !entityOK || !revisionOK || !payloadOK {
			return fmt.Errorf("invalid ProjectConfiguration outbox value types")
		}
		configuration, canonical, err := MigrateProjectConfigurationPayload(payload)
		if err != nil {
			return fmt.Errorf("migrate ProjectConfiguration outbox %q: %w", id, err)
		}
		if configuration.ProjectID != entityID || int64(configuration.Revision) != revision {
			return fmt.Errorf("ProjectConfiguration outbox %q conflicts with its payload", id)
		}
		statements = append(statements, upstream.Statement{SQL: `UPDATE hub_outbox SET payload=? WHERE id=?`, Args: []any{canonical, id}})
	}
	revisions, err := d.Shared.Query(ctx, `SELECT entity_id,project_id,revision,payload FROM shared_entity_revisions WHERE entity_type='project_configuration' ORDER BY entity_id,revision LIMIT ?`, int64(projectConfigurationHardCutMaxRows+1))
	if err != nil {
		return err
	}
	if len(revisions.Rows) > projectConfigurationHardCutMaxRows {
		return fmt.Errorf("ProjectConfiguration revision migration exceeds bounded row maximum")
	}
	for _, row := range revisions.Rows {
		if len(row) != 4 {
			return fmt.Errorf("invalid Shared ProjectConfiguration revision row")
		}
		entityID, entityOK := row[0].(string)
		projectID, projectOK := row[1].(string)
		revision, revisionOK := row[2].(int64)
		payload, payloadOK := row[3].([]byte)
		if !payloadOK {
			if text, ok := row[3].(string); ok {
				payload, payloadOK = []byte(text), true
			}
		}
		if !entityOK || !projectOK || !revisionOK || !payloadOK {
			return fmt.Errorf("invalid Shared ProjectConfiguration revision value types")
		}
		configuration, canonical, err := MigrateProjectConfigurationPayload(payload)
		if err != nil {
			return fmt.Errorf("migrate Shared ProjectConfiguration revision: %w", err)
		}
		if configuration.ProjectID != projectID || configuration.ProjectID != entityID || int64(configuration.Revision) != revision {
			return fmt.Errorf("Shared ProjectConfiguration revision conflicts with its payload")
		}
		statements = append(statements, upstream.Statement{SQL: `UPDATE shared_entity_revisions SET payload=? WHERE entity_type='project_configuration' AND entity_id=? AND revision=?`, Args: []any{canonical, entityID, revision}})
	}
	for offset := 0; offset < len(statements); offset += projectConfigurationHardCutBatchSize {
		end := offset + projectConfigurationHardCutBatchSize
		if end > len(statements) {
			end = len(statements)
		}
		if _, err := d.Shared.Batch(ctx, statements[offset:end]); err != nil {
			return fmt.Errorf("migrate canonical ProjectConfiguration payloads: %w", err)
		}
	}
	if err := d.setSharedUpgradeMigrationState(ctx, projectConfigurationHardCutMigrationID, "complete"); err != nil {
		return err
	}
	return nil
}
