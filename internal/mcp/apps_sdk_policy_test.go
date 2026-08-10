package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestWorkflowPolicyMutationFailsClosedWithoutTrustedAuthorityMCP(t *testing.T) {
	tools := (&Server{Service: service.New(config.Config{})}).tools()
	for _, name := range []string{"project_workflow_policy_adopt", "project_workflow_policy_update"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("%s is not exposed to Planner/Delivery", name)
		}
	}
	read, ok := tools["project_workflow_policy_read"]
	if !ok || read.Annotations != readOnlyAnnotations() {
		t.Fatalf("workflow policy read is not the permitted public surface: %#v", read)
	}
	response := callMCP(t, &Server{Service: service.New(config.Config{})}, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "project_workflow_policy_adopt", "arguments": map[string]any{"policy": map[string]any{}}},
	}))
	result, ok := response["result"].(map[string]any)
	if !ok && response["error"] == nil {
		t.Fatalf("missing trusted authority was accepted: %#v", response)
	}
	if ok && result["isError"] != true {
		t.Fatalf("missing trusted authority was accepted: %#v", response)
	}
	if ok {
		text := result["content"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, "AUTHORITY_UNAVAILABLE") {
			t.Fatalf("unexpected unavailable-authority error: %q", text)
		}
	}
}

func validWorkflowPolicyArgument() map[string]any {
	return map[string]any{
		"schema_version":     1,
		"project_id":         "example",
		"revision":           1,
		"workflow_stage":     "transitional_main",
		"integration_branch": "main",
		"agent":              map[string]any{"wait_for_ci": false},
		"ci":                 map[string]any{"task": "disabled", "task_merge": "observe", "release": "observe"},
		"gates":              []string{"format", "check", "test"},
		"updated_by":         "test",
		"updated_at":         "2026-08-07T00:00:00Z",
	}
}

func TestWorkflowPolicyMutationSchemasAreClosedAndNestedStrict(t *testing.T) {
	tools := (&Server{Service: service.New(config.Config{})}).tools()
	for _, name := range []string{"project_workflow_policy_adopt", "project_workflow_policy_update"} {
		tool := tools[name]
		properties := tool.InputSchema["properties"].(map[string]any)
		policySchema := properties["policy"].(map[string]any)
		if policySchema["additionalProperties"] != false {
			t.Fatalf("%s policy schema is open: %#v", name, policySchema)
		}
		for _, field := range []string{"schema_version", "project_id", "revision", "workflow_stage", "integration_branch", "agent", "ci", "updated_by", "updated_at"} {
			if !containsString(stringList(policySchema["required"]), field) {
				t.Fatalf("%s policy schema omitted required field %q", name, field)
			}
		}
		policyProperties := policySchema["properties"].(map[string]any)
		for _, field := range []string{"agent", "ci"} {
			nested := policyProperties[field].(map[string]any)
			if nested["additionalProperties"] != false {
				t.Fatalf("%s nested %s schema is open: %#v", name, field, nested)
			}
		}
		ci := policyProperties["ci"].(map[string]any)
		for _, field := range []string{"task", "task_merge", "release"} {
			if !containsString(stringList(ci["required"]), field) {
				t.Fatalf("%s policy ci schema omitted required field %q", name, field)
			}
		}
		gates, ok := policyProperties["gates"].(map[string]any)
		if !ok || gates["type"] != "array" {
			t.Fatalf("%s policy schema omitted project-owned gates: %#v", name, policyProperties["gates"])
		}
	}
}

func TestWorkflowPolicyMutationPolicyDecodeAndModelValidationAfterAuthorityBoundary(t *testing.T) {
	valid := validWorkflowPolicyArgument()
	valid["unexpected"] = true
	raw, err := json.Marshal(map[string]any{"policy": valid})
	if err != nil {
		t.Fatal(err)
	}
	var in service.ProjectWorkflowPolicyInput
	if err := decode(raw, &in); err == nil {
		t.Fatal("policy decoder accepted unknown nested field")
	}
	missing := validWorkflowPolicyArgument()
	delete(missing["ci"].(map[string]any), "release")
	raw, err = json.Marshal(map[string]any{"policy": missing})
	if err != nil {
		t.Fatal(err)
	}
	in = service.ProjectWorkflowPolicyInput{}
	if err := decode(raw, &in); err != nil {
		t.Fatalf("policy decoder rejected structurally valid missing-field input: %v", err)
	}
	if err := model.ValidateProjectWorkflowPolicy(in.Policy); err == nil {
		t.Fatal("policy model validation accepted missing nested field")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
