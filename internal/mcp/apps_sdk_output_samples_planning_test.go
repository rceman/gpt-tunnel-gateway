package mcp

import (
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (f canonicalOutputFixture) canonicalPlanningOutputSamples() map[string]any {
	adr := f.adr.(model.ADR)
	task := f.task.(model.Task)
	state := f.state.(model.TaskState)
	transaction := f.transaction.(hub.TransactionResult)
	return map[string]any{
		"project_register": f.operation,
		"plan_read":        f.plan, "plan_cutover": f.operation, "plan_update": f.operation, "plan_section_read": f.section, "plan_section_create": f.operation, "plan_section_update": f.operation, "plan_section_delete": f.operation, "plan_render": f.render, "plan_history": map[string]any{"history": []map[string]string{{"sha": transaction.After, "date": f.now.Format(time.RFC3339), "author": "GPT", "subject": "subject"}}, "next_cursor": "", "has_more": false},
		"adr_list": map[string]any{"adrs": []model.ADR{adr}, "next_cursor": "", "has_more": false}, "adr_read": adr, "adr_create": f.operation,
		"task_create": map[string]any{"task": task, "operation": f.operation}, "task_list": map[string]any{"tasks": []service.TaskRecord{{Task: task, State: state, RunSummaries: []model.RunReviewSummary{}}}, "next_cursor": "", "has_more": false}, "task_read": f.publicPacket,
		"task_revision_list": map[string]any{"revisions": []any{f.revisionSample}, "next_cursor": "", "has_more": false}, "task_revision_read": f.revisionSample, "task_revision_status": f.revisionStatusSample,
		"task_correction_create": map[string]any{"revision": f.revisionSample, "operation": f.operation},
		"task_train_status":      map[string]any{"project_id": "project", "train_id": "current", "status": "active", "current_index": 0, "task_count": 2, "current_task_id": task.ID, "current_run_id": "", "current_task_state": state.Status, "current_run_status": "", "agent_state": "", "wait_reason": "", "next_task_id": "TSK-SECOND", "tail": "", "next_cursor": "", "has_more": false},
		"task_supersede":         map[string]any{"task": f.task, "operation": f.operation}, "task_cancel": f.operation,
		"task_mark_merge_ready": f.operation, "task_defer": f.operation, "task_mark_merged": f.operation,
	}
}
