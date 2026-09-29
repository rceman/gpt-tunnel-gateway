package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK666Gate20SharedConfigurationOutboxRevisionMigration(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.setSharedUpgradeMigrationState(ctx, projectConfigurationHardCutMigrationID, "complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, projectConfigurationRetiredFieldMigrationID); err != nil {
		t.Fatal(err)
	}
	if err := db.setSharedUpgradeMigrationState(ctx, projectConfigurationRetiredFieldMigrationID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	configuration := model.DefaultProjectConfiguration("example", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	currentRetired := projectConfigurationRetiredFieldsFixture(t, data)
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = 1
	fields["execution_model"] = "train_v2"
	delete(fields, "guide_bindings")
	integration := fields["integration"].(map[string]any)
	integration["target_branch"] = "main"
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	legacy = projectConfigurationRetiredFieldsFixture(t, legacy)
	secondConfiguration := configuration
	secondConfiguration.Revision++
	secondConfiguration.UpdatedAt = configuration.UpdatedAt.Add(time.Minute)
	secondData, err := json.Marshal(secondConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	secondRetired := projectConfigurationRetiredFieldsFixture(t, secondData)
	now := configuration.UpdatedAt.Format(time.RFC3339Nano)
	secondNow := secondConfiguration.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, configuration.ProjectID, configuration.Revision, currentRetired, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "config-migration-outbox", "project_configuration", configuration.ProjectID, configuration.ProjectID, configuration.Revision, "project-configuration-update", legacy, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "config-migration-outbox-2", "project_configuration", secondConfiguration.ProjectID, secondConfiguration.ProjectID, secondConfiguration.Revision, "project-configuration-update", secondRetired, secondNow); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at,published_at) VALUES(?,?,?,?,?,?,?,?,?)`, "config-migration-published", "project_configuration", secondConfiguration.ProjectID, secondConfiguration.ProjectID, 1, "project-configuration-update", []byte(`not-json`), secondNow, secondNow); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", configuration.ProjectID, configuration.ProjectID, configuration.Revision, "update", "planner", "initial", []byte(`[]`), currentRetired, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", secondConfiguration.ProjectID, secondConfiguration.ProjectID, secondConfiguration.Revision, "update", "planner", "follow-up", []byte(`[]`), secondRetired, secondNow); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		name string
		sql  string
		args []any
	}{
		{name: "current", sql: `SELECT payload FROM shared_project_configurations WHERE id=?`, args: []any{configuration.ProjectID}},
		{name: "outbox-v1", sql: `SELECT payload FROM hub_outbox WHERE id=?`, args: []any{"config-migration-outbox"}},
		{name: "outbox-v2", sql: `SELECT payload FROM hub_outbox WHERE id=?`, args: []any{"config-migration-outbox-2"}},
		{name: "history-v1", sql: `SELECT payload FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? AND revision=?`, args: []any{configuration.ProjectID, configuration.Revision}},
		{name: "history-v2", sql: `SELECT payload FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? AND revision=?`, args: []any{secondConfiguration.ProjectID, secondConfiguration.Revision}},
	} {
		rows, err := db.Shared.Query(ctx, query.sql, query.args...)
		if err != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
			t.Fatalf("%s payload query rows=%#v err=%v", query.name, rows, err)
		}
		payload, ok := rows.Rows[0][0].([]byte)
		if !ok {
			t.Fatalf("%s payload has type %T", query.name, rows.Rows[0][0])
		}
		got, err := DecodeCanonicalProjectConfigurationPayload(payload)
		if err != nil || got.SchemaVersion != model.ProjectConfigurationSchemaVersion || bytes.Contains(payload, []byte(`"execution_model"`)) || bytes.Contains(payload, []byte(`"watcher"`)) || bytes.Contains(payload, []byte(`"workflow"`)) {
			t.Fatalf("%s payload was not canonical: %#v err=%v", query.name, got, err)
		}
		if got.GuideBindings == nil || got.Procedures == nil || got.Hooks == nil || got.Integration.TargetBranch == "" {
			t.Fatalf("%s payload missed migrated defaults: %#v", query.name, got)
		}
	}
	published, err := db.Shared.Query(ctx, `SELECT payload FROM hub_outbox WHERE id=?`, "config-migration-published")
	publishedPayload, payloadOK := []byte(nil), false
	if err == nil && len(published.Rows) == 1 {
		publishedPayload, payloadOK = published.Rows[0][0].([]byte)
	}
	if err != nil || !payloadOK || !bytes.Equal(publishedPayload, []byte(`not-json`)) {
		t.Fatalf("published immutable outbox history was migrated: rows=%#v err=%v", published, err)
	}
	state, err := db.sharedUpgradeMigrationState(ctx, projectConfigurationRetiredFieldMigrationID)
	if err != nil || state != "complete" {
		t.Fatalf("retired-field migration state=%q err=%v", state, err)
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
}

func TestProjectConfigurationCallbackMigrationRequiresSemanticMapping(t *testing.T) {
	configuration := model.DefaultProjectConfiguration("example", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = 2
	fields["callbacks"] = []any{map[string]any{
		"callback": "legacy_notify",
		"event":    "agent.work_finished",
		"url":      map[string]any{"method": "POST", "url": "https://example.invalid/hook", "body": "{}"},
	}}
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := MigrateProjectConfigurationPayload(legacy); err == nil {
		t.Fatal("legacy callback without explicit semantic Procedure mapping was migrated")
	}
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"epoch":   map[string]any{"type": "string", "minLength": 47, "maxLength": 47},
			"project": map[string]any{"$ref": "EntityKeyAndReference"},
			"agent":   map[string]any{"$ref": "EntityKeyAndReference"},
		},
		"required":             []any{"epoch", "project"},
		"additionalProperties": false,
	}
	fields["procedures"] = map[string]any{
		"notify": map[string]any{
			"script": "scripts/notify.sh", "summary": "Notify after Agent work", "guide": "Sends the project's semantic work-finished notification.",
			"input":  input,
			"output": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		},
	}
	fields["hooks"] = map[string]string{model.HookPostAgentWorkFinished: "notify"}
	mapped, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	configuration, canonical, err := MigrateProjectConfigurationPayload(mapped)
	if err != nil || configuration.Hooks[model.HookPostAgentWorkFinished] != "notify" {
		t.Fatalf("explicit callback Procedure mapping failed: configuration=%#v err=%v", configuration, err)
	}
	if bytes.Contains(canonical, []byte(`"callbacks"`)) {
		t.Fatalf("canonical ProjectConfiguration retained callbacks: %s", canonical)
	}
}

func projectConfigurationRetiredFieldsFixture(t *testing.T, data []byte) []byte {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["watcher"] = map[string]any{"enabled": true, "interval_seconds": 15}
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestProjectConfigurationHardCutRejectsUnknownRetiredForms(t *testing.T) {
	configuration := model.DefaultProjectConfiguration("example", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["execution_model"] = "future_mode"
	invalid, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := MigrateProjectConfigurationPayload(invalid); err == nil {
		t.Fatal("unsupported execution model was accepted")
	}
	if _, err := DecodeCanonicalProjectConfigurationPayload(invalid); err == nil {
		t.Fatal("canonical decoder accepted a retired execution model")
	}
}

func TestTSK675ExactGTWLegacyGateConfigurationMigratesAcrossSharedSurfaces(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	base := model.DefaultProjectConfiguration("gpt-tunnel-gateway", now)
	if err := db.SeedSharedRulesFromConfiguration(ctx, base, "GTW"); err != nil {
		t.Fatal(err)
	}
	if err := db.setSharedUpgradeMigrationState(ctx, projectConfigurationHardCutMigrationID, "complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, projectConfigurationRetiredFieldMigrationID); err != nil {
		t.Fatal(err)
	}
	if err := db.setSharedUpgradeMigrationState(ctx, projectConfigurationRetiredFieldMigrationID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	policy := model.ProjectWorkflowPolicy{
		SchemaVersion: model.SchemaVersion, ProjectID: base.ProjectID, Revision: 1,
		WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main",
		Agent:     model.WorkflowPolicyAgent{WaitForCI: false},
		CI:        model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeDisabled, Release: model.WorkflowCIModeDisabled},
		UpdatedBy: base.UpdatedBy, UpdatedAt: base.UpdatedAt,
	}
	legacyPayload := func(revision int, updatedAt time.Time) []byte {
		t.Helper()
		configuration := base
		configuration.Revision = revision
		configuration.UpdatedAt = updatedAt
		data, err := json.Marshal(configuration)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		fields["schema_version"] = json.RawMessage(`2`)
		fields["activation_profile_ref"] = json.RawMessage(`"default"`)
		fields["watcher"] = json.RawMessage(`{"enabled":true,"interval_seconds":15}`)
		integration := map[string]json.RawMessage{}
		if err := json.Unmarshal(fields["integration"], &integration); err != nil {
			t.Fatal(err)
		}
		integration["pre"] = json.RawMessage(`{"command":["./scripts/integration_activate.py","pre"]}`)
		integration["post"] = json.RawMessage(`{"command":["./scripts/integration_activate.py","post"]}`)
		fields["integration"], err = json.Marshal(integration)
		if err != nil {
			t.Fatal(err)
		}
		workflow, err := json.Marshal(map[string]any{
			"workflow_stage": policy.WorkflowStage, "integration_branch": policy.IntegrationBranch,
			"wait_for_ci": policy.Agent.WaitForCI, "ci": policy.CI, "gates": model.StandardWorkflowGates(),
			"gate_commands": tsk675ExactLegacyGateCommands(),
		})
		if err != nil {
			t.Fatal(err)
		}
		fields["workflow"] = workflow
		payload, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	firstTime := now.UTC()
	secondTime := now.Add(time.Minute).UTC()
	first := legacyPayload(1, firstTime)
	second := legacyPayload(2, secondTime)
	rejectIntegration := func(pre json.RawMessage, message string) {
		t.Helper()
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(first, &fields); err != nil {
			t.Fatal(err)
		}
		var integration map[string]json.RawMessage
		if err := json.Unmarshal(fields["integration"], &integration); err != nil {
			t.Fatal(err)
		}
		integration["pre"] = pre
		integrationPayload, err := json.Marshal(integration)
		if err != nil {
			t.Fatal(err)
		}
		fields["integration"] = integrationPayload
		legacy, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := MigrateProjectConfigurationPayloadWithPolicy(legacy, &policy); err == nil {
			t.Fatal(message)
		}
	}
	rejectIntegration(json.RawMessage(`{"command":"unsafe"}`), "non-array legacy integration command was accepted")
	rejectIntegration(json.RawMessage(`{"command":[]}`), "empty legacy integration argv was accepted")
	rejectIntegration(json.RawMessage(`{"command":["./scripts/integration_activate.py","pre\n"]}`), "unsafe legacy integration argv was accepted")
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, base.ProjectID, 1, first, firstTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, outbox := range []struct {
		id       string
		revision int
		payload  []byte
		created  time.Time
	}{{"gtw-config-current", 1, first, firstTime}, {"gtw-config-pending", 2, second, secondTime}} {
		if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, outbox.id, "project_configuration", base.ProjectID, base.ProjectID, outbox.revision, "project-configuration-update", outbox.payload, outbox.created.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	for _, history := range []struct {
		revision int
		payload  []byte
		recorded time.Time
	}{{1, first, firstTime}, {2, second, secondTime}} {
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", base.ProjectID, base.ProjectID, history.revision, "update", "planner", "legacy configuration", []byte(`[]`), history.payload, history.recorded.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		name string
		sql  string
		args []any
	}{
		{"current", `SELECT payload FROM shared_project_configurations WHERE id=?`, []any{base.ProjectID}},
		{"outbox-current", `SELECT payload FROM hub_outbox WHERE id=?`, []any{"gtw-config-current"}},
		{"outbox-pending", `SELECT payload FROM hub_outbox WHERE id=?`, []any{"gtw-config-pending"}},
		{"history-current", `SELECT payload FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? AND revision=1`, []any{base.ProjectID}},
		{"history-pending", `SELECT payload FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? AND revision=2`, []any{base.ProjectID}},
	}
	for _, query := range queries {
		rows, err := db.Shared.Query(ctx, query.sql, query.args...)
		if err != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
			t.Fatalf("%s canonical payload rows=%#v err=%v", query.name, rows, err)
		}
		payload, ok := rows.Rows[0][0].([]byte)
		if !ok {
			t.Fatalf("%s payload type=%T", query.name, rows.Rows[0][0])
		}
		configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
		if err != nil || configuration.Hooks[model.HookPreTaskVerify] != "task_verify" || configuration.Procedures["task_verify"].Script != "scripts/task-verify.py" || configuration.Integration.TargetBranch != "main" {
			t.Fatalf("%s did not receive canonical Procedure mapping: configuration=%#v err=%v", query.name, configuration, err)
		}
		for _, retired := range [][]byte{[]byte(`"activation_profile_ref"`), []byte(`"gate_commands"`), []byte(`"watcher"`), []byte(`"workflow"`)} {
			if bytes.Contains(payload, retired) {
				t.Fatalf("%s retained retired field %s", query.name, retired)
			}
		}
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("idempotent migration replay: %v", err)
	}
}

func TestTSK675GTWMigrationRejectsNoncanonicalGateCommandMapping(t *testing.T) {
	base := model.DefaultProjectConfiguration("gpt-tunnel-gateway", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	policy := model.ProjectWorkflowPolicy{
		SchemaVersion: model.SchemaVersion, ProjectID: base.ProjectID, Revision: 1,
		WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main",
		Agent:     model.WorkflowPolicyAgent{WaitForCI: false},
		CI:        model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeDisabled, Release: model.WorkflowCIModeDisabled},
		UpdatedBy: base.UpdatedBy, UpdatedAt: base.UpdatedAt,
	}
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = json.RawMessage(`2`)
	commands := tsk675ExactLegacyGateCommands()
	commands.Test.Task.Command = []string{"go", "test", "./..."}
	workflow, err := json.Marshal(map[string]any{
		"workflow_stage": policy.WorkflowStage, "integration_branch": policy.IntegrationBranch,
		"wait_for_ci": policy.Agent.WaitForCI, "ci": policy.CI, "gates": model.StandardWorkflowGates(),
		"gate_commands": commands,
	})
	if err != nil {
		t.Fatal(err)
	}
	fields["workflow"] = workflow
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := MigrateProjectConfigurationPayloadWithPolicy(legacy, &policy); err == nil {
		t.Fatal("noncanonical GTW gate command was mapped into a Procedure")
	}
}

func tsk675ExactLegacyGateCommands() model.ProjectGateCommands {
	return model.ProjectGateCommands{
		Format: model.ProjectGateCommand{Command: []string{"go", "run", "./cmd/gofmt-struct", "--check", "."}},
		Check:  model.ProjectGateCommand{Command: []string{"python3", "scripts/static-check.py"}},
		Test: model.ProjectGateTestCommands{
			Task: model.ProjectGateCommand{Command: []string{"go", "test", "./...", "-count=1"}},
		},
	}
}
