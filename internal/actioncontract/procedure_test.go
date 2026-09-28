package actioncontract

import "testing"

func TestDynamicProcedureActionCompilesAgainstCanonicalReferences(t *testing.T) {
	contracts, err := LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task":     map[string]any{"$ref": "EntityKeyAndReference"},
			"revision": map[string]any{"$ref": "Revision"},
		},
		"required":             []any{"task"},
		"additionalProperties": false,
	}
	output := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"accepted": map[string]any{"type": "boolean"}},
		"required":             []any{"accepted"},
		"additionalProperties": false,
	}
	action, err := contracts.CompileProcedureAction("procedure/read", "Read task detail", "Reads one bounded task.", input, output)
	if err != nil {
		t.Fatal(err)
	}
	if action.Path != "procedure/read" || action.Guide != "Reads one bounded task." || action.Metadata.Annotations.ReadOnly || !action.Metadata.Annotations.Destructive || action.Metadata.Annotations.Idempotent || !action.Metadata.Annotations.OpenWorld {
		t.Fatalf("unexpected dynamic action contract: %#v", action)
	}
	if err := ValidateCompiledActionInput(action, map[string]any{"task": "GTW-TSK602"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompiledActionInput(action, map[string]any{"task": "GTW-TSK602", "other": true}); err == nil {
		t.Fatal("unknown input property was accepted")
	}
	if err := ValidateCompiledActionOutput(action, map[string]any{"operation": "GTW-OPR123", "status": "accepted"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompiledActionOutput(action, map[string]any{"operation": "GTW-OPR123", "status": "completed", "result": map[string]any{"accepted": true}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompiledActionOutput(action, map[string]any{"operation": "GTW-OPR123", "status": "accepted", "other": true}); err == nil {
		t.Fatal("unknown output property was accepted")
	}
}

func TestDynamicProcedureActionRejectsInvalidPathsAndSchema(t *testing.T) {
	contracts, err := LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	for _, path := range []string{"procedure/read-more", "procedure/Read", "procedure/read/extra", "config/read"} {
		if _, err := contracts.CompileProcedureAction(path, "summary", "guide", root, root); err == nil {
			t.Fatalf("invalid dynamic action path %q was accepted", path)
		}
	}
	open := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}
	if _, err := contracts.CompileProcedureAction("procedure/read", "summary", "guide", open, root); err == nil {
		t.Fatal("open Procedure schema was accepted")
	}
}
