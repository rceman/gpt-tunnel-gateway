package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestDurableHandoffMCPRegistersExactCanonicalTools(t *testing.T) {
	tools := (&Server{Service: service.New(config.Config{})}).tools()
	want := []string{
		"delivery_handoff_publish", "delivery_handoff_read", "delivery_handoff_status", "delivery_handoff_list",
		"delivery_handoff_acknowledge", "delivery_handoff_next", "delivery_handoff_cancel", "delivery_handoff_supersede",
		"planner_report_publish", "planner_report_read", "planner_report_status", "planner_report_list",
		"planner_report_acknowledge", "planner_report_next",
	}
	for _, name := range want {
		if _, ok := tools[name]; !ok {
			t.Fatalf("canonical durable tool is not registered: %s", name)
		}
	}
	for _, name := range []string{"delivery_handoff_create", "planner_report_resolve"} {
		if _, ok := tools[name]; ok {
			t.Fatalf("non-canonical durable tool alias is registered: %s", name)
		}
	}
}

func durableHandoffAuthorityArguments(name string) map[string]any {
	ownerSummary := map[string]any{
		"status": "working", "goal": "goal", "currently_doing": "doing", "why_it_matters": "matters",
		"completed_so_far": []string{}, "next_step": "next", "owner_action_required": nil,
	}
	base := map[string]any{
		"project_id": "example", "task_id": "EXM-TSK1", "run_id": "EXM-TSK1-RUN1", "owner_summary": ownerSummary,
		"technical_evidence": map[string]any{"terminal": false}, "plan_revision": 1, "hub_revision": strings.Repeat("a", 40),
		"task_refs": []map[string]any{{"task_id": "EXM-TSK1", "task_sha256": strings.Repeat("b", 64)}}, "train_refs": []string{},
		"first_action": "first", "stop_boundary": "stop", "prohibited_operations": []string{}, "instruction_body": "instruction", "created_by": "planner",
	}
	switch name {
	case "delivery_handoff_publish":
		return base
	case "delivery_handoff_cancel":
		return map[string]any{"handoff_id": "handoff", "cancelled_by": "planner", "reason": "reason"}
	case "delivery_handoff_supersede":
		base["handoff_id"] = "handoff"
		delete(base, "project_id")
		delete(base, "task_id")
		delete(base, "run_id")
		return base
	case "delivery_handoff_acknowledge":
		return map[string]any{"handoff_id": "handoff", "acknowledged_by": "delivery"}
	case "delivery_handoff_next":
		return map[string]any{"handoff_id": "handoff", "next_by": "delivery"}
	case "planner_report_acknowledge":
		return map[string]any{"report_id": "report", "acknowledged_by": "planner"}
	case "planner_report_next":
		return map[string]any{"report_id": "report", "resolved_by": "planner"}
	case "planner_report_publish":
		return map[string]any{
			"handoff_id": "handoff",
			"report": map[string]any{
				"report_type": "blocked", "owner_summary": map[string]any{
					"status": "blocked", "goal": "goal", "currently_doing": "doing", "why_it_matters": "matters", "completed_so_far": []string{}, "next_step": "next", "owner_action_required": nil,
				}, "technical_evidence": map[string]any{}, "published_by": "delivery",
			},
		}
	default:
		return map[string]any{}
	}
}

func mcpResultText(response map[string]any) string {
	result, ok := response["result"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func TestDurableHandoffMCPUsesOnlyServerAuthorityContext(t *testing.T) {
	plannerServer := &Server{
		Service:          service.New(config.Config{}),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	deliveryServer := &Server{
		Service:          service.New(config.Config{}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	plannerTools := []string{"delivery_handoff_publish", "delivery_handoff_cancel", "delivery_handoff_supersede", "planner_report_acknowledge", "planner_report_next"}
	for _, name := range plannerTools {
		args := durableHandoffAuthorityArguments(name)
		body := mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": name, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args, "_meta": map[string]any{"role": "planner"}}})
		deliveryText := mcpResultText(callMCP(t, deliveryServer, body))
		if !strings.Contains(deliveryText, "AUTHORITY_UNAVAILABLE") {
			t.Fatalf("Delivery-bound server allowed Planner mutation %s: %q", name, deliveryText)
		}
		body = mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": name + "-planner", "method": "tools/call", "params": map[string]any{"name": name, "arguments": args, "_meta": map[string]any{"role": "delivery"}}})
		plannerText := mcpResultText(callMCP(t, plannerServer, body))
		if strings.Contains(plannerText, "AUTHORITY_UNAVAILABLE") {
			t.Fatalf("Planner-bound server ignored its authority for %s: %q", name, plannerText)
		}
	}

	for _, name := range []string{"delivery_handoff_acknowledge", "delivery_handoff_next", "planner_report_publish"} {
		args := durableHandoffAuthorityArguments(name)
		body := mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": name + "-planner", "method": "tools/call", "params": map[string]any{"name": name, "arguments": args, "_meta": map[string]any{"role": "delivery"}}})
		plannerText := mcpResultText(callMCP(t, plannerServer, body))
		if !strings.Contains(plannerText, "AUTHORITY_UNAVAILABLE") {
			t.Fatalf("Planner-bound server allowed Delivery mutation %s: %q", name, plannerText)
		}
		body = mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": name + "-delivery", "method": "tools/call", "params": map[string]any{"name": name, "arguments": args, "_meta": map[string]any{"role": "planner"}}})
		deliveryText := mcpResultText(callMCP(t, deliveryServer, body))
		if strings.Contains(deliveryText, "AUTHORITY_UNAVAILABLE") {
			t.Fatalf("Delivery-bound server ignored its authority for %s: %q", name, deliveryText)
		}
	}
}

func TestDurableHandoffMCPRejectsMalformedUnauthorizedMutationBeforeDecode(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	for _, name := range []string{"delivery_handoff_publish", "delivery_handoff_cancel", "planner_report_acknowledge"} {
		body := mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": name, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": map[string]any{"unknown": map[string]any{"malformed": true}}},
		})
		response := callMCP(t, server, body)
		text := mcpResultText(response)
		if !strings.Contains(text, "AUTHORITY_UNAVAILABLE") {
			t.Fatalf("malformed unauthorized mutation %s was decoded before auth: %q response=%#v", name, text, response)
		}
	}
	unauthenticated := &Server{Service: service.New(config.Config{})}
	for _, name := range []string{"project_onboard", "project_onboard_recover", "project_workflow_policy_adopt", "project_workflow_policy_update"} {
		body := mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": name, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": map[string]any{"unknown": map[string]any{"malformed": true}}},
		})
		response := callMCP(t, unauthenticated, body)
		text := mcpResultText(response)
		if !strings.Contains(text, "AUTHORITY_UNAVAILABLE") {
			t.Fatalf("malformed unauthenticated mutation %s was decoded before auth: %q response=%#v", name, text, response)
		}
	}
}

func TestDurableHandoffMCPSchemasAreClosedAndBounded(t *testing.T) {
	tools := (&Server{Service: service.New(config.Config{MaxListItems: 2})}).tools()
	owner := tools["delivery_handoff_publish"].InputSchema["properties"].(map[string]any)["owner_summary"].(map[string]any)
	ownerProperties := owner["properties"].(map[string]any)
	if owner["additionalProperties"] != false || !containsString(stringList(owner["required"]), "owner_action_required") {
		t.Fatalf("owner summary is not closed/nullable-required: %#v", owner)
	}
	if ownerProperties["status"].(map[string]any)["enum"] == nil || ownerProperties["completed_so_far"].(map[string]any)["maxItems"] != 3 {
		t.Fatalf("owner summary bounds are missing: %#v", ownerProperties)
	}
	report := tools["planner_report_publish"].InputSchema["properties"].(map[string]any)["report"].(map[string]any)
	reportType := report["properties"].(map[string]any)["report_type"].(map[string]any)["enum"]
	if len(stringList(reportType)) != 3 || !containsString(stringList(reportType), "completed") || !containsString(stringList(reportType), "blocked") || !containsString(stringList(reportType), "decision_required") {
		t.Fatalf("planner report type is not closed: %#v", reportType)
	}
	if _, err := service.New(config.Config{MaxListItems: 2}).DeliveryHandoffList(context.Background(), service.DeliveryHandoffListInput{ProjectID: "example", Limit: 3}); err == nil {
		t.Fatal("handoff list accepted a limit above Config.MaxListItems")
	}
	if _, err := service.New(config.Config{MaxListItems: 2}).PlannerReportList(context.Background(), service.PlannerReportListInput{ProjectID: "example", Limit: 3}); err == nil {
		t.Fatal("report list accepted a limit above Config.MaxListItems")
	}
}
