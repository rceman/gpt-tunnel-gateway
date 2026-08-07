package mcp

import "testing"

func schemaObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema value has type %T, want object", value)
	}
	return object
}

func TestBoundedReadOutputSchemas(t *testing.T) {
	plan := schemaObject(t, toolOutputSchemas["plan_read"])
	planProperties := schemaObject(t, plan["properties"])
	sections := schemaObject(t, planProperties["sections"])
	section := schemaObject(t, sections["items"])
	sectionProperties := schemaObject(t, section["properties"])
	for _, forbidden := range []string{"description", "body", "content"} {
		if _, ok := sectionProperties[forbidden]; ok {
			t.Fatalf("plan_read section schema exposes full-detail field %q", forbidden)
		}
	}
	if required := schemaObject(t, plan)["required"]; required == nil {
		t.Fatal("plan_read schema has no required fields")
	}

	status := schemaObject(t, toolOutputSchemas["project_status"])
	variants, ok := status["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("project_status schema variants = %#v, want baseline and delta", status["oneOf"])
	}
	for i, variant := range variants {
		if _, ok := schemaObject(t, variant)["additionalProperties"]; !ok {
			t.Fatalf("project_status variant %d is not closed", i)
		}
	}
}
