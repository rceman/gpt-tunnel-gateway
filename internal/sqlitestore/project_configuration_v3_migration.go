package sqlitestore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func MigrateProjectConfigurationPayloadWithPolicy(data []byte, policy *model.ProjectWorkflowPolicy) (model.ProjectConfiguration, []byte, error) {
	if len(data) == 0 || len(data) > projectConfigurationHardCutMaxPayloadBytes {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("ProjectConfiguration migration payload exceeds bounds")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("decode ProjectConfiguration migration payload")
	}
	var version int
	if err := json.Unmarshal(fields["schema_version"], &version); err != nil {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("invalid ProjectConfiguration schema version")
	}
	delete(fields, "watcher")
	if version == model.ProjectConfigurationSchemaVersion {
		canonicalInput, err := json.Marshal(fields)
		if err != nil {
			return model.ProjectConfiguration{}, nil, err
		}
		configuration, err := DecodeCanonicalProjectConfigurationPayload(canonicalInput)
		if err != nil {
			return model.ProjectConfiguration{}, nil, err
		}
		canonical, err := json.Marshal(configuration)
		return configuration, canonical, err
	}
	if version != 1 && version != 2 {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("unsupported ProjectConfiguration schema version %d", version)
	}
	var projectID string
	if err := json.Unmarshal(fields["project_id"], &projectID); err != nil || model.ValidateProjectIdentifier(projectID) != nil {
		return model.ProjectConfiguration{}, nil, fmt.Errorf("invalid ProjectConfiguration migration identity")
	}
	allowed := map[string]struct{}{
		"schema_version": {}, "project_id": {}, "revision": {}, "execution_model": {}, "agent_routing": {},
		"workflow": {}, "checkpoint": {}, "integration": {}, "guide_bindings": {}, "callbacks": {},
		"activation_profile_ref": {}, "updated_by": {}, "updated_at": {}, "watcher": {}, "procedures": {}, "hooks": {},
		"commands": {}, "automation": {}, "targets": {}, "default_target": {}, "defaults": {},
	}
	for name := range fields {
		if _, ok := allowed[name]; !ok {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("unknown ProjectConfiguration migration field %q", name)
		}
	}
	for _, name := range []string{"commands", "automation", "targets", "default_target", "defaults"} {
		if raw, ok := fields[name]; ok {
			empty, err := migrationValueEmpty(raw)
			if err != nil || !empty {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy %s authority requires an explicit Procedure migration", name)
			}
			delete(fields, name)
		}
	}
	if raw, ok := fields["execution_model"]; ok {
		var value string
		if version != 1 || json.Unmarshal(raw, &value) != nil || value != "legacy" && value != "train_v2" {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("unsupported retired ProjectConfiguration execution model")
		}
		delete(fields, "execution_model")
	}
	delete(fields, "watcher")
	if raw, ok := fields["callbacks"]; ok {
		var callbacks []json.RawMessage
		if err := json.Unmarshal(raw, &callbacks); err != nil {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("invalid legacy callback configuration")
		}
		if len(callbacks) != 0 {
			var hooks map[string]string
			var procedures map[string]json.RawMessage
			if json.Unmarshal(fields["hooks"], &hooks) != nil || json.Unmarshal(fields["procedures"], &procedures) != nil {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy callbacks require an explicit post_agent_work_finished Procedure mapping")
			}
			procedureName := hooks[model.HookPostAgentWorkFinished]
			if model.ValidateProcedureName(procedureName) != nil {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy callbacks require an explicit post_agent_work_finished Procedure mapping")
			}
			if _, exists := procedures[procedureName]; !exists {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy callback Procedure mapping does not resolve")
			}
			for _, callback := range callbacks {
				var legacy struct {
					Event string `json:"event"`
				}
				if json.Unmarshal(callback, &legacy) != nil || legacy.Event != "agent.work_finished" {
					return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy callback event has no canonical Hook mapping")
				}
			}
		}
		delete(fields, "callbacks")
	}
	if raw, ok := fields["activation_profile_ref"]; ok {
		var reference string
		if err := json.Unmarshal(raw, &reference); err != nil || reference != "" && (projectID != "gpt-tunnel-gateway" || reference != "default") {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy activation profile is unsupported")
		}
		delete(fields, "activation_profile_ref")
	}
	if raw, ok := fields["workflow"]; ok {
		if policy == nil {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("canonical ProjectWorkflowPolicy is required to migrate legacy workflow data")
		}
		mapped, err := migrateLegacyWorkflow(raw, fields, *policy)
		if err != nil {
			return model.ProjectConfiguration{}, nil, err
		}
		if mapped {
			if err := installGTWTaskVerificationProcedure(fields); err != nil {
				return model.ProjectConfiguration{}, nil, err
			}
		}
		delete(fields, "workflow")
	}
	if raw, ok := fields["integration"]; ok {
		var integration map[string]json.RawMessage
		if err := json.Unmarshal(raw, &integration); err != nil || integration == nil {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("invalid legacy integration configuration")
		}
		for name, value := range integration {
			if name == "target_branch" {
				var branch string
				if json.Unmarshal(value, &branch) != nil || branch == "" || policy != nil && branch != policy.IntegrationBranch {
					return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy integration target conflicts with canonical ProjectWorkflowPolicy")
				}
				continue
			}
			if name != "pre" && name != "post" && name != "command" && name != "commands" {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("unknown legacy integration field %q", name)
			}
			empty, err := migrationValueEmpty(value)
			if err != nil {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("invalid legacy integration field %q", name)
			}
			if !empty {
				if (name != "pre" && name != "post") || projectID != "gpt-tunnel-gateway" || validateRetiredIntegrationCommandObject(value) != nil {
					return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy integration argv has no canonical migration")
				}
			}
			delete(integration, name)
		}
		if _, ok := integration["target_branch"]; !ok && policy != nil {
			integration["target_branch"], _ = json.Marshal(policy.IntegrationBranch)
		}
		encoded, err := json.Marshal(integration)
		if err != nil {
			return model.ProjectConfiguration{}, nil, err
		}
		fields["integration"] = encoded
	}
	fields["schema_version"] = mustMigrationJSON(model.ProjectConfigurationSchemaVersion)
	for name, value := range map[string]json.RawMessage{
		"checkpoint":     mustMigrationJSON(map[string]any{}),
		"guide_bindings": mustMigrationJSON(map[string]string{}),
		"procedures":     mustMigrationJSON(map[string]model.ProjectProcedureDefinition{}),
		"hooks":          mustMigrationJSON(map[string]string{}),
	} {
		if _, exists := fields[name]; !exists {
			fields[name] = value
		}
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
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return model.ProjectConfiguration{}, nil, err
	}
	canonical, err := json.Marshal(configuration)
	if err != nil {
		return model.ProjectConfiguration{}, nil, err
	}
	return configuration, canonical, nil
}

func ProjectConfigurationPayloadRequiresWorkflowPolicy(data []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	var version int
	if json.Unmarshal(fields["schema_version"], &version) != nil || version == model.ProjectConfigurationSchemaVersion {
		return false
	}
	workflow, ok := fields["workflow"]
	return ok && len(bytes.TrimSpace(workflow)) > 0 && !bytes.Equal(bytes.TrimSpace(workflow), []byte("null"))
}

func migrateLegacyWorkflow(raw json.RawMessage, projectFields map[string]json.RawMessage, policy model.ProjectWorkflowPolicy) (bool, error) {
	var workflow map[string]json.RawMessage
	if err := json.Unmarshal(raw, &workflow); err != nil || workflow == nil {
		return false, fmt.Errorf("invalid legacy workflow configuration")
	}
	var projectID string
	if err := json.Unmarshal(projectFields["project_id"], &projectID); err != nil || policy.ProjectID != projectID {
		return false, fmt.Errorf("canonical ProjectWorkflowPolicy identity does not match legacy configuration")
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return false, fmt.Errorf("canonical ProjectWorkflowPolicy is invalid: %w", err)
	}
	allowed := map[string]bool{
		"workflow_stage": true, "integration_branch": true, "wait_for_ci": true, "agent": true, "ci": true,
		"gates": true, "gate_commands": true, "task_submission": true, "task_verification": true,
	}
	for name := range workflow {
		if !allowed[name] {
			return false, fmt.Errorf("unknown legacy workflow field %q", name)
		}
	}
	mapped := false
	if rawCommands, ok := workflow["gate_commands"]; ok {
		empty, err := migrationValueEmpty(rawCommands)
		if err != nil {
			return false, fmt.Errorf("invalid legacy workflow gate_commands")
		}
		if !empty {
			if projectID != "gpt-tunnel-gateway" || !matchesLegacyGTWTaskGateCommands(rawCommands) {
				return false, fmt.Errorf("legacy workflow gate_commands have no exact Procedure migration")
			}
			mapped = true
		}
		delete(workflow, "gate_commands")
	}
	if rawGates, ok := workflow["gates"]; ok {
		var gates []string
		if err := decodeMigrationStrict(rawGates, &gates); err != nil || model.ValidateWorkflowGates(gates) != nil || !reflect.DeepEqual(model.EffectiveProjectWorkflowGates(gates), model.StandardWorkflowGates()) {
			return false, fmt.Errorf("legacy workflow gates do not match the canonical verification Procedure")
		}
		if len(gates) > 0 && (!mapped || projectID != "gpt-tunnel-gateway") {
			return false, fmt.Errorf("legacy workflow gates have no exact Procedure migration")
		}
		delete(workflow, "gates")
	}
	for _, name := range []string{"task_submission", "task_verification"} {
		if value, ok := workflow[name]; ok {
			empty, err := migrationValueEmpty(value)
			if err != nil || !empty {
				return false, fmt.Errorf("legacy workflow %s requires explicit migration", name)
			}
			delete(workflow, name)
		}
	}
	var stage, branch string
	if decodeMigrationStrict(workflow["workflow_stage"], &stage) != nil || stage == "" {
		return false, fmt.Errorf("legacy workflow policy is incomplete: workflow_stage")
	}
	if decodeMigrationStrict(workflow["integration_branch"], &branch) != nil || branch == "" {
		return false, fmt.Errorf("legacy workflow policy is incomplete: integration_branch")
	}
	waitForCI, waitFound := false, false
	if rawWait, ok := workflow["wait_for_ci"]; ok {
		if string(bytes.TrimSpace(rawWait)) != "true" && string(bytes.TrimSpace(rawWait)) != "false" || json.Unmarshal(rawWait, &waitForCI) != nil {
			return false, fmt.Errorf("invalid legacy workflow policy wait_for_ci")
		}
		waitFound = true
	}
	if rawAgent, ok := workflow["agent"]; ok {
		var agent struct {
			WaitForCI *bool `json:"wait_for_ci"`
		}
		if decodeMigrationStrict(rawAgent, &agent) != nil || agent.WaitForCI == nil {
			return false, fmt.Errorf("legacy workflow policy is incomplete: agent.wait_for_ci")
		}
		if waitFound && waitForCI != *agent.WaitForCI {
			return false, fmt.Errorf("legacy workflow wait_for_ci fields conflict")
		}
		waitForCI = *agent.WaitForCI
		waitFound = true
	}
	if !waitFound {
		return false, fmt.Errorf("legacy workflow policy is incomplete: wait_for_ci")
	}
	var ci model.WorkflowPolicyCI
	if decodeMigrationStrict(workflow["ci"], &ci) != nil || ci.Task == "" || ci.TaskMerge == "" || ci.Release == "" {
		return false, fmt.Errorf("legacy workflow policy is incomplete: ci")
	}
	if stage != policy.WorkflowStage || branch != policy.IntegrationBranch || waitForCI != policy.Agent.WaitForCI || ci != policy.CI {
		return false, fmt.Errorf("legacy workflow conflicts with canonical ProjectWorkflowPolicy")
	}
	return mapped, nil
}

func installGTWTaskVerificationProcedure(fields map[string]json.RawMessage) error {
	input, ok := model.ProjectHookPayloadSchema(model.HookPreTaskVerify)
	if !ok {
		return fmt.Errorf("canonical pre_task_verify payload is unavailable")
	}
	output, err := model.TaskVerificationProcedureOutputSchema([]string{"format", "static_check", "full_test"})
	if err != nil {
		return err
	}
	definition := model.ProjectProcedureDefinition{
		Script:  "scripts/task-verify.py",
		Summary: "Verify the exact Task candidate with the canonical project checks.",
		Guide:   "Runs whole-tree keyed-struct formatting, repository static checks, and the canonical full test suite in order; returns bounded structured gate evidence.",
		Input:   input,
		Output:  output,
	}
	procedures := map[string]model.ProjectProcedureDefinition{}
	if raw, ok := fields["procedures"]; ok {
		if err := decodeMigrationStrict(raw, &procedures); err != nil || procedures == nil {
			return fmt.Errorf("invalid legacy Procedure catalogue")
		}
	}
	const name = "task_verify"
	if existing, exists := procedures[name]; exists {
		current, _ := json.Marshal(existing)
		wanted, _ := json.Marshal(definition)
		if !bytes.Equal(current, wanted) {
			return fmt.Errorf("existing task_verify Procedure conflicts with the canonical migration")
		}
	}
	procedures[name] = definition
	hooks := map[string]string{}
	if raw, ok := fields["hooks"]; ok {
		if err := decodeMigrationStrict(raw, &hooks); err != nil || hooks == nil {
			return fmt.Errorf("invalid legacy Hook bindings")
		}
	}
	if existing, exists := hooks[model.HookPreTaskVerify]; exists && existing != name {
		return fmt.Errorf("existing pre_task_verify binding conflicts with the canonical migration")
	}
	hooks[model.HookPreTaskVerify] = name
	procedurePayload, err := json.Marshal(procedures)
	if err != nil {
		return err
	}
	hookPayload, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	fields["procedures"] = procedurePayload
	fields["hooks"] = hookPayload
	return nil
}

func matchesLegacyGTWTaskGateCommands(raw json.RawMessage) bool {
	var commands model.ProjectGateCommands
	if decodeMigrationStrict(raw, &commands) != nil {
		return false
	}
	expected := model.ProjectGateCommands{
		Format: model.ProjectGateCommand{Command: []string{"go", "run", "./cmd/gofmt-struct", "--check", "."}},
		Check:  model.ProjectGateCommand{Command: []string{"python3", "scripts/static-check.py"}},
		Test: model.ProjectGateTestCommands{
			Task: model.ProjectGateCommand{Command: []string{"go", "test", "./...", "-count=1"}},
		},
	}
	return reflect.DeepEqual(commands, expected)
}

func validateRetiredIntegrationCommandObject(raw json.RawMessage) error {
	var command struct {
		Argv []string `json:"command"`
	}
	if err := decodeMigrationStrict(raw, &command); err != nil || len(command.Argv) == 0 || len(command.Argv) > 64 {
		return fmt.Errorf("invalid retired integration command")
	}
	for _, arg := range command.Argv {
		if len(arg) == 0 || len(arg) > 1024 || bytes.ContainsAny([]byte(arg), "\x00\r\n") {
			return fmt.Errorf("invalid retired integration command")
		}
	}
	return nil
}

func decodeMigrationStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("unexpected migration JSON suffix")
	}
	return nil
}

func migrationValueEmpty(raw json.RawMessage) (bool, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return false, err
	}
	return migrationValueIsEmpty(value), nil
}

func migrationValueIsEmpty(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case bool:
		return !value
	case string:
		return value == ""
	case json.Number:
		return value == "0"
	case []any:
		return len(value) == 0
	case map[string]any:
		return len(value) == 0
	default:
		return reflect.ValueOf(value).IsZero()
	}
}

func mustMigrationJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
