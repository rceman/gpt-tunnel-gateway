package service

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestProjectConfigurationHubMigrationFailsClosedOnRetiredGateCommands(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := context.Background()
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = json.RawMessage(`2`)
	fields["watcher"] = json.RawMessage(`{"enabled":true,"interval_seconds":15}`)
	policy, err := s.ProjectWorkflowPolicyRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := json.Marshal(tsk675LegacyWorkflow(policy))
	if err != nil {
		t.Fatal(err)
	}
	fields["workflow"] = workflow
	retired, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	path := s.projectConfigurationPath("example")
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := s.Hub.Transact(ctx, revision, "test: seed retired Hub ProjectConfiguration fields", func(worktree string) ([]string, error) {
		if err := hub.WriteText(worktree, path, string(retired)); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateHubProjectConfigurations(ctx); err == nil {
		t.Fatal("nonempty retired gate-command authority was migrated without an explicit Procedure mapping")
	}
	currentRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil || currentRevision != seeded.After {
		t.Fatalf("failed migration changed Hub revision: got=%q seeded=%q err=%v", currentRevision, seeded.After, err)
	}
	var current json.RawMessage
	if err := s.Hub.ReadJSON(ctx, path, &current); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(current), bytes.TrimSpace(retired)) {
		t.Fatalf("failed migration mutated the legacy configuration: %s", current)
	}
}

func TestTSK675HubMigratesExactGTWVerificationProfile(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	db := testServiceWithDurability(t, s)
	ctx := context.Background()
	const projectID = "gpt-tunnel-gateway"
	projectConfig := s.Config.Projects["example"]
	projectConfig.ProjectCode = "GTW"
	projectConfig.Root = t.TempDir()
	projectConfig.Mirror = filepath.Join(projectConfig.Root, "gpt-tunnel-gateway.git")
	projectConfig.AirelaySessionKey = projectID + "_master"
	s.Config.Projects[projectID] = projectConfig
	hubRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectRegister(ctx, ProjectRegisterInput{
		Project: model.Project{
			SchemaVersion: 1, ID: projectID, RepositoryURL: "git@example.invalid:gpt-tunnel-gateway.git", DefaultBranch: "main",
			WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "b1a45b1e9475ab29dfd3e84d523b70897c7b8918", Status: "active",
		},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err != nil {
		t.Fatal(err)
	}
	var configuration model.ProjectConfiguration
	path := s.projectConfigurationPath(projectID)
	if err := s.Hub.ReadJSON(ctx, path, &configuration); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedSharedRulesFromConfiguration(ctx, configuration, "GTW"); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: projectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_project_identifiers(project_id,project_code) VALUES(?,?)`, projectID, "GTW"); err != nil {
		t.Fatal(err)
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = json.RawMessage(`2`)
	fields["activation_profile_ref"] = json.RawMessage(`"default"`)
	fields["watcher"] = json.RawMessage(`{"enabled":true,"interval_seconds":15}`)
	var integration map[string]json.RawMessage
	if err := json.Unmarshal(fields["integration"], &integration); err != nil {
		t.Fatal(err)
	}
	integration["pre"] = json.RawMessage(`{"command":["./scripts/integration_activate.py","pre"]}`)
	integration["post"] = json.RawMessage(`{"command":["./scripts/integration_activate.py","post"]}`)
	fields["integration"], err = json.Marshal(integration)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := json.Marshal(tsk675LegacyWorkflow(policy))
	if err != nil {
		t.Fatal(err)
	}
	fields["workflow"] = workflow
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	hubRevision, err = s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(ctx, hubRevision, "test: seed exact GTW ProjectConfiguration v2", func(worktree string) ([]string, error) {
		if err := hub.WriteText(worktree, path, string(legacy)); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateHubProjectConfigurations(ctx); err != nil {
		t.Fatal(err)
	}
	var migrated model.ProjectConfiguration
	var migratedRaw json.RawMessage
	if err := s.Hub.ReadJSON(ctx, path, &migratedRaw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(migratedRaw, &migrated); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateProjectConfiguration(migrated); err != nil || migrated.SchemaVersion != model.ProjectConfigurationSchemaVersion || migrated.Hooks[model.HookPreTaskVerify] != "task_verify" || len(migrated.Hooks) != 1 || len(migrated.Procedures) != 1 {
		t.Fatalf("GTW Hub migration did not install canonical task verification: configuration=%#v err=%v", migrated, err)
	}
	procedure, found := migrated.Procedures["task_verify"]
	if !found || procedure.Script != "scripts/task-verify.py" || migrated.Integration.TargetBranch != policy.IntegrationBranch {
		t.Fatalf("GTW Hub migration lost canonical Procedure or integration policy: configuration=%#v", migrated)
	}
	for _, retired := range [][]byte{[]byte(`"activation_profile_ref"`), []byte(`"gate_commands"`), []byte(`"watcher"`), []byte(`"workflow"`)} {
		if bytes.Contains(migratedRaw, retired) {
			t.Fatalf("GTW Hub migration retained retired field %s: %s", retired, migratedRaw)
		}
	}
}

func tsk675LegacyWorkflow(policy model.ProjectWorkflowPolicy) map[string]any {
	return map[string]any{
		"workflow_stage":     policy.WorkflowStage,
		"integration_branch": policy.IntegrationBranch,
		"wait_for_ci":        policy.Agent.WaitForCI,
		"ci":                 policy.CI,
		"gates":              model.StandardWorkflowGates(),
		"gate_commands": model.ProjectGateCommands{
			Format: model.ProjectGateCommand{Command: []string{"go", "run", "./cmd/gofmt-struct", "--check", "."}},
			Check:  model.ProjectGateCommand{Command: []string{"python3", "scripts/static-check.py"}},
			Test: model.ProjectGateTestCommands{
				Task: model.ProjectGateCommand{Command: []string{"go", "test", "./...", "-count=1"}},
			},
		},
	}
}
