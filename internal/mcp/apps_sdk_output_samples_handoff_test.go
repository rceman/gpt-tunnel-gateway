package mcp

func (f canonicalOutputFixture) canonicalHandoffOutputSamples() map[string]any {
	return map[string]any{
		"delivery_handoff_publish":     map[string]any{"handoff": f.handoff, "operation": f.operation},
		"delivery_handoff_read":        f.handoff,
		"delivery_handoff_status":      f.handoffStatus,
		"delivery_handoff_list":        map[string]any{"handoffs": []any{f.handoffStatus}},
		"delivery_handoff_acknowledge": map[string]any{"handoff": f.handoff, "operation": f.operation},
		"delivery_handoff_next":        map[string]any{"handoff": f.handoff, "operation": f.operation},
		"delivery_handoff_cancel":      map[string]any{"handoff": f.handoff, "operation": f.operation},
		"delivery_handoff_supersede":   map[string]any{"handoff": f.handoff, "operation": f.operation},
		"planner_report_publish":       map[string]any{"report": f.plannerReport, "operation": f.operation},
		"planner_report_read":          f.plannerReport,
		"planner_report_status":        f.plannerReportStatus,
		"planner_report_list":          map[string]any{"reports": []any{f.plannerReportStatus}},
		"planner_report_acknowledge":   map[string]any{"state": f.plannerReportState, "operation": f.operation},
		"planner_report_next":          map[string]any{"state": f.plannerReportState, "operation": f.operation},
	}
}
