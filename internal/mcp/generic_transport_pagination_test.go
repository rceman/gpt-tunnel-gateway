package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func registerTransportProbeActions(t *testing.T, server *Server) {
	t.Helper()
	output := closedOutput(map[string]any{
		"items":       outputArray(outputString()),
		"payload":     map[string]any{"type": "object", "additionalProperties": true},
		"nested":      map[string]any{"type": "object", "properties": map[string]any{"_pagination": outputString(), "_metrics": outputString()}},
		"_pagination": map[string]any{"type": "object", "properties": map[string]any{"next_cursor": outputString()}},
		"_metrics":    map[string]any{"type": "object"},
	}, "items")
	input := obj(map[string]any{})
	register := func(path string, execute func(context.Context, any) (any, error)) {
		err := server.RegisterGenericAction(GenericAction{
			Path:             path,
			Description:      "transport probe",
			InputSchema:      input,
			OutputSchema:     output,
			AuthorityRole:    actionRolePlannerOrAgent,
			LocalReceiptOnly: true,
			Execute: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return execute(ctx, nil)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	register("probe/page", func(context.Context, any) (any, error) {
		return map[string]any{
			"items": []any{"one"},
			"payload": map[string]any{
				"_pagination": "nested-pagination",
				"_metrics":    "nested-metrics",
				"value":       "preserved",
			},
			"_pagination": map[string]any{"next_cursor": "opaque-1"},
			"_metrics":    map[string]any{"private": true},
		}, nil
	})
	register("probe/terminal", func(context.Context, any) (any, error) {
		return map[string]any{"items": []any{"terminal"}, "payload": map[string]any{"value": "preserved"}}, nil
	})
	register("probe/failure", func(context.Context, any) (any, error) {
		return nil, fmt.Errorf("probe failure")
	})
}

func TestGenericCallPublicDetachesContinuationAndPreservesPayload(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	registerTransportProbeActions(t, server)

	response, err := server.genericCallPublic(server.AuthorityContext, nil, mustJSON(t, map[string]any{
		"session": sessionID, "action": "probe/page", "input": map[string]any{},
	}))
	if err != nil {
		t.Fatal(err)
	}
	responseMap := response.(map[string]any)
	if got := responseMap["pagination"]; !reflect.DeepEqual(got, map[string]any{"next_cursor": "opaque-1"}) {
		t.Fatalf("unexpected top-level pagination: %#v", responseMap)
	}
	result := responseMap["result"].(map[string]any)
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

func TestGenericCallPublicTerminalAndFailureOmitPagination(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	registerTransportProbeActions(t, server)

	for _, action := range []string{"probe/terminal", "probe/failure"} {
		response, err := server.genericCallPublic(server.AuthorityContext, nil, mustJSON(t, map[string]any{
			"session": sessionID, "action": action, "input": map[string]any{},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := response.(map[string]any)["pagination"]; ok {
			t.Fatalf("%s unexpectedly returned pagination: %#v", action, response)
		}
	}
}

func TestGenericTransportSchemaSanitizesOnlyRootMetadata(t *testing.T) {
	service, _ := mcpServiceWithSQLite(t, config.Config{GatewayID: "transport-schema"})
	server := &Server{Service: service}
	registerTransportProbeActions(t, server)
	entry := server.genericActionRegistry(nil)["probe/page"]
	properties := entry.OutputSchema["properties"].(map[string]any)
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
