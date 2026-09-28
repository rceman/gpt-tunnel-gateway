package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProjectConfigurationV3DefaultsValidate(t *testing.T) {
	configuration := DefaultProjectConfiguration("example", time.Unix(10, 0).UTC())
	if err := ValidateProjectConfiguration(configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.SchemaVersion != 3 || configuration.AgentRouting.Fallback != ReasoningBestAvailable || configuration.Integration.TargetBranch != "main" {
		t.Fatalf("unexpected defaults: %#v", configuration)
	}
	if configuration.Checkpoint.Adapter != "" || len(configuration.Procedures) != 0 || len(configuration.Hooks) != 0 {
		t.Fatalf("generic defaults selected project execution behavior: %#v", configuration)
	}
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{`"workflow"`, `"callbacks"`, `"activation_profile_ref"`, `"gate_commands"`} {
		if strings.Contains(string(data), retired) {
			t.Fatalf("v3 configuration contains retired field %s: %s", retired, data)
		}
	}
}

func TestProjectProcedureSchemaSubsetAndHookCompatibility(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project": map[string]any{"$ref": "EntityKeyAndReference"},
			"task":    map[string]any{"$ref": "EntityKeyAndReference"},
			"stage":   map[string]any{"type": "string", "enum": []any{"code", "rebase"}},
		},
		"required":             []any{"project", "task", "stage"},
		"additionalProperties": false,
	}
	output := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"passed": map[string]any{"type": "boolean"}},
		"required":             []any{"passed"},
		"additionalProperties": false,
	}
	definition := ProjectProcedureDefinition{
		Script:  "scripts/task-submit.sh",
		Summary: "Validate Task submission",
		Guide:   "Runs bounded submission checks.",
		Input:   input,
		Output:  output,
	}
	if err := ValidateProjectProcedureDefinition(definition); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProcedureHookCompatibility(HookPreTaskSubmit, input); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProjectHooks(map[string]string{HookPreTaskSubmit: "task_submit"}, map[string]ProjectProcedureDefinition{"task_submit": definition}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskVerificationProcedureSchemaAllowsOnlyCanonicalGateID(t *testing.T) {
	output, err := TaskVerificationProcedureOutputSchema([]string{"format", "full_test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateProcedureSchema(output); err != nil {
		t.Fatalf("canonical Task verification output schema was rejected: %v", err)
	}
	invalid := map[string]any{
		"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required": []any{"id"}, "additionalProperties": false,
	}
	if err := ValidateProcedureSchema(invalid); err == nil {
		t.Fatal("unscoped id property was accepted")
	}
	properties := output["properties"].(map[string]any)
	gates := properties["gates"].(map[string]any)
	item := gates["items"].(map[string]any)
	item["properties"].(map[string]any)["unexpected"] = map[string]any{"type": "string"}
	if err := ValidateProcedureSchema(output); err == nil {
		t.Fatal("noncanonical Task verification gate record was accepted")
	}
}

func TestProjectProcedureSchemaRejectsOpenOrUnsupportedShapes(t *testing.T) {
	validRoot := func(properties map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	}
	tests := map[string]map[string]any{
		"open object":         {"type": "object", "properties": map[string]any{}, "additionalProperties": true},
		"unsupported keyword": validRoot(map[string]any{"value": map[string]any{"type": "string", "pattern": "^x"}}),
		"identity alias":      validRoot(map[string]any{"project_id": map[string]any{"$ref": "EntityKeyAndReference"}}),
		"wrong entity field":  validRoot(map[string]any{"owner": map[string]any{"$ref": "EntityKeyAndReference"}}),
		"unknown reference":   validRoot(map[string]any{"task": map[string]any{"$ref": "Task"}}),
		"array reference":     validRoot(map[string]any{"tasks": map[string]any{"type": "array", "items": map[string]any{"$ref": "EntityKeyAndReference"}}}),
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateProcedureSchema(schema); err == nil {
				t.Fatal("invalid Procedure schema was accepted")
			}
		})
	}
}

func TestProjectProcedureHookCompatibilityFailsClosed(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"epoch":   map[string]any{"type": "string", "minLength": 47, "maxLength": 47},
			"project": map[string]any{"$ref": "EntityKeyAndReference"},
			"agent":   map[string]any{"$ref": "EntityKeyAndReference"},
		},
		"required":             []any{"epoch", "project", "agent"},
		"additionalProperties": false,
	}
	if err := ValidateProcedureSchema(input); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProcedureHookCompatibility(HookPostAgentWorkFinished, input); err == nil {
		t.Fatal("required optional Agent was considered compatible")
	}
	input["required"] = []any{"epoch", "project"}
	if err := ValidateProcedureHookCompatibility(HookPostAgentWorkFinished, input); err != nil {
		t.Fatal(err)
	}
	input["required"] = []any{"project", "agent"}
	if err := ValidateProcedureHookCompatibility(HookPreTaskSubmit, input); err == nil {
		t.Fatal("required property absent from canonical payload was accepted")
	}
}

func TestProjectHookInventoryIsClosedAndOrdered(t *testing.T) {
	want := []string{
		HookPreTaskSubmit,
		HookPostTaskSubmit,
		HookPreTaskVerify,
		HookPostTaskVerify,
		HookPreTaskIntegrate,
		HookPostTaskIntegrate,
		HookPostAgentWorkFinished,
	}
	got := ProjectHookNames()
	if len(got) != len(want) {
		t.Fatalf("Hook inventory=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Hook inventory=%v", got)
		}
	}
	if IsProjectHookName("pre_agent_work_finished") || IsProjectHookName("custom_hook") {
		t.Fatal("noncanonical Hook name accepted")
	}
}
