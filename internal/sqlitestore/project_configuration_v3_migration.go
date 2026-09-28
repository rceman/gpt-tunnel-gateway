package sqlitestore

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		if err := json.Unmarshal(raw, &reference); err != nil || reference != "" {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy activation profile requires an explicit Procedure migration")
		}
		delete(fields, "activation_profile_ref")
	}
	if raw, ok := fields["workflow"]; ok {
		if policy == nil {
			return model.ProjectConfiguration{}, nil, fmt.Errorf("canonical ProjectWorkflowPolicy is required to migrate legacy workflow data")
		}
		if err := validateLegacyWorkflow(raw, fields, *policy); err != nil {
			return model.ProjectConfiguration{}, nil, err
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
				continue
			}
			if name != "pre" && name != "post" && name != "command" && name != "commands" {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("unknown legacy integration field %q", name)
			}
			empty, err := migrationValueEmpty(value)
			if err != nil || !empty {
				return model.ProjectConfiguration{}, nil, fmt.Errorf("legacy integration argv requires an explicit Procedure migration")
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

func validateLegacyWorkflow(raw json.RawMessage, projectFields map[string]json.RawMessage, policy model.ProjectWorkflowPolicy) error {
	var workflow map[string]json.RawMessage
	if err := json.Unmarshal(raw, &workflow); err != nil || workflow == nil {
		return fmt.Errorf("invalid legacy workflow configuration")
	}
	allowed := map[string]bool{
		"workflow_stage": true, "integration_branch": true, "wait_for_ci": true, "agent": true, "ci": true,
		"gates": true, "gate_commands": true, "task_submission": true, "task_verification": true,
	}
	for name, value := range workflow {
		if !allowed[name] {
			return fmt.Errorf("unknown legacy workflow field %q", name)
		}
		if name == "gates" || name == "gate_commands" || name == "task_submission" || name == "task_verification" {
			empty, err := migrationValueEmpty(value)
			if err != nil || !empty {
				return fmt.Errorf("legacy workflow %s requires explicit migration", name)
			}
			delete(workflow, name)
		}
	}
	projectID := ""
	if err := json.Unmarshal(projectFields["project_id"], &projectID); err != nil || policy.ProjectID != projectID {
		return fmt.Errorf("canonical ProjectWorkflowPolicy identity does not match legacy configuration")
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return fmt.Errorf("canonical ProjectWorkflowPolicy is invalid: %w", err)
	}
	var stage, branch string
	var waitForCI bool
	var ci model.WorkflowPolicyCI
	if err := json.Unmarshal(workflow["workflow_stage"], &stage); err != nil || stage == "" {
		return fmt.Errorf("legacy workflow policy is incomplete: workflow_stage")
	}
	if err := json.Unmarshal(workflow["integration_branch"], &branch); err != nil || branch == "" {
		return fmt.Errorf("legacy workflow policy is incomplete: integration_branch")
	}
	if rawWait, ok := workflow["wait_for_ci"]; ok {
		if err := json.Unmarshal(rawWait, &waitForCI); err != nil {
			return fmt.Errorf("invalid legacy workflow policy wait_for_ci")
		}
	} else if rawAgent, ok := workflow["agent"]; ok {
		var agent struct {
			WaitForCI *bool `json:"wait_for_ci"`
		}
		if err := json.Unmarshal(rawAgent, &agent); err != nil || agent.WaitForCI == nil {
			return fmt.Errorf("legacy workflow policy is incomplete: agent.wait_for_ci")
		}
		waitForCI = *agent.WaitForCI
	} else {
		return fmt.Errorf("legacy workflow policy is incomplete: wait_for_ci")
	}
	if err := json.Unmarshal(workflow["ci"], &ci); err != nil || ci.Task == "" || ci.TaskMerge == "" || ci.Release == "" {
		return fmt.Errorf("legacy workflow policy is incomplete: ci")
	}
	if stage != policy.WorkflowStage || branch != policy.IntegrationBranch || waitForCI != policy.Agent.WaitForCI || ci != policy.CI {
		return fmt.Errorf("legacy workflow conflicts with canonical ProjectWorkflowPolicy")
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
