package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGenericCallEnvelopeDetachesContinuationAndPreservesPayload(t *testing.T) {
	result, pagination, err := detachPrivateTransportMetadata(map[string]any{
		"items": []any{"one"},
		"payload": map[string]any{
			"_pagination": "nested-pagination",
			"_metrics":    "nested-metrics",
			"value":       "preserved",
		},
		"_pagination": map[string]any{"next_cursor": "ABCDEFGH"},
		"_metrics":    map[string]any{"private": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := genericActionSuccessWithPagination(result, pagination)
	if got := response["pagination"]; !reflect.DeepEqual(got, map[string]any{"next_cursor": "ABCDEFGH"}) {
		t.Fatalf("unexpected top-level pagination: %#v", response)
	}
	result = response["result"].(map[string]any)
	if _, ok := result["_pagination"]; ok {
		t.Fatalf("private pagination leaked into public result: %#v", result)
	}
	if _, ok := result["_metrics"]; ok {
		t.Fatalf("private metrics leaked into public result: %#v", result)
	}
	wantPayload := map[string]any{"_pagination": "nested-pagination", "_metrics": "nested-metrics", "value": "preserved"}
	if !reflect.DeepEqual(result["payload"], wantPayload) {
		t.Fatalf("nested payload changed: got=%#v want=%#v", result["payload"], wantPayload)
	}
}

func TestGenericCallTerminalAndFailureOmitPagination(t *testing.T) {
	terminal, err := genericActionPageResult(map[string]any{"items": []any{"terminal"}}, false, "ABCDEFGH")
	if err != nil {
		t.Fatal(err)
	}
	for _, response := range []map[string]any{
		genericActionSuccessWithPagination(terminal.Result, terminal.Pagination),
		genericActionError("probe/failure", "probe failure"),
	} {
		if _, ok := response["pagination"]; ok {
			t.Fatalf("terminal or failed response unexpectedly returned pagination: %#v", response)
		}
	}
}

func TestGenericTransportPaginationIsOutsideTheCollectionResult(t *testing.T) {
	page, err := genericActionPageResult(map[string]any{"refs": []any{}}, true, "ABCDEFGH")
	if err != nil {
		t.Fatal(err)
	}
	structured := genericActionSuccessWithPagination(page.Result, page.Pagination)
	if !reflect.DeepEqual(structured["result"], map[string]any{"refs": []any{}}) {
		t.Fatalf("unexpected collection result: %#v", structured)
	}
	if !reflect.DeepEqual(structured["pagination"], map[string]any{"next_cursor": "ABCDEFGH"}) {
		t.Fatalf("unexpected outer pagination: %#v", structured)
	}
	result := structured["result"].(map[string]any)
	for _, field := range []string{"next_cursor", "has_more", "_pagination"} {
		if _, exists := result[field]; exists {
			t.Fatalf("collection result retained continuation field %q", field)
		}
	}
}

func TestGenericTransportSchemaSanitizesOnlyRootMetadata(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items":       map[string]any{"type": "array"},
			"_pagination": map[string]any{"type": "object"},
			"_metrics":    map[string]any{"type": "object"},
			"nested": map[string]any{"type": "object", "properties": map[string]any{
				"_pagination": outputString(), "_metrics": outputString(),
			}},
		},
	}
	encoded, err := json.Marshal(sanitizeTransportOutputSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	var sanitized map[string]any
	if err := json.Unmarshal(encoded, &sanitized); err != nil {
		t.Fatal(err)
	}
	properties := sanitized["properties"].(map[string]any)
	if _, ok := properties["_pagination"]; ok {
		t.Fatal("root _pagination remained in registered output schema")
	}
	if _, ok := properties["_metrics"]; ok {
		t.Fatal("root _metrics remained in registered output schema")
	}
	nested := properties["nested"].(map[string]any)["properties"].(map[string]any)
	if _, ok := nested["_pagination"]; !ok {
		t.Fatal("nested _pagination was incorrectly removed")
	}
	if _, ok := nested["_metrics"]; !ok {
		t.Fatal("nested _metrics was incorrectly removed")
	}
}

func TestGenericActionRegistrationRejectsUnfrozenPath(t *testing.T) {
	server := newSessionTestServer(t)
	err := server.RegisterGenericAction(GenericAction{Path: "probe/page"})
	if err == nil || err.Error() != `generic action "probe/page" has no canonical action contract` {
		t.Fatalf("unfrozen action registration error=%v", err)
	}
}
