package mcp

func handoffToolOutputSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"delivery_handoff_publish":     closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
		"delivery_handoff_read":        deliveryHandoffSchema(),
		"delivery_handoff_status":      deliveryHandoffStatusSchema(),
		"delivery_handoff_list":        closedOutput(map[string]any{"handoffs": outputArray(deliveryHandoffStatusSchema())}, "handoffs"),
		"delivery_handoff_acknowledge": closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
		"delivery_handoff_next":        closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
		"delivery_handoff_cancel":      closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
		"delivery_handoff_supersede":   closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
		"planner_report_publish":       closedOutput(map[string]any{"report": plannerReportSchema(), "operation": operationOutputSchema()}, "report", "operation"),
		"planner_report_read":          plannerReportSchema(),
		"planner_report_status":        plannerReportStatusSchema(),
		"planner_report_list":          closedOutput(map[string]any{"reports": outputArray(plannerReportStatusSchema())}, "reports"),
		"planner_report_acknowledge":   closedOutput(map[string]any{"state": plannerReportStateSchema(), "operation": operationOutputSchema()}, "state", "operation"),
		"planner_report_next":          closedOutput(map[string]any{"state": plannerReportStateSchema(), "operation": operationOutputSchema()}, "state", "operation"),
	}
}
