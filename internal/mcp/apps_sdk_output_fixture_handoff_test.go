package mcp

import (
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (f *canonicalOutputFixture) populateCanonicalHandoff() {
	now := f.now
	task := f.task.(model.Task)
	run := f.run.(model.Run)
	transaction := f.transaction.(hub.TransactionResult)
	handoffSummary := map[string]any{"status": "working", "goal": "goal", "currently_doing": "doing", "why_it_matters": "matters", "completed_so_far": []string{"done"}, "next_step": "next", "owner_action_required": nil}
	handoff := map[string]any{"schema_version": 1, "id": "handoff", "project_id": "project", "task_id": task.ID, "run_id": run.ID, "task_sha256": task.SHA256, "status": "in_progress", "owner_summary": handoffSummary, "technical_evidence": map[string]any{"terminal": false}, "plan_revision": 1, "hub_revision": transaction.After, "task_refs": []map[string]any{{"task_id": task.ID, "task_sha256": task.SHA256}}, "train_refs": []string{"train-1"}, "plan_section_refs": []string{}, "operator_event_refs": []string{}, "expected_repo_base": "", "expected_repo_head": "", "first_action": "first", "stop_boundary": "stop", "prohibited_operations": []string{"release"}, "instruction_body": "instructions", "role_refs": []string{"planner"}, "delegation_refs": []string{"delivery"}, "author_role": "planner", "consumer_role": "delivery", "canonical_digest": strings.Repeat("a", 64), "created_by": "planner", "created_at": now, "updated_at": now}
	handoffStatus := map[string]any{"schema_version": 1, "id": "handoff", "project_id": "project", "task_id": task.ID, "run_id": run.ID, "status": "in_progress", "owner_summary": handoffSummary, "created_at": now, "updated_at": now}
	reportEvidence := map[string]any{"blocker_class": "dependency", "severity": "low", "failed_precondition": "precondition", "verified_facts": []string{"fact"}, "preservation_resume": "resume", "same_run_correction_available": false}
	plannerReport := map[string]any{"schema_version": 1, "id": "report", "project_id": "project", "handoff_id": "handoff", "task_id": task.ID, "run_id": run.ID, "task_sha256": task.SHA256, "report_type": "blocked", "owner_summary": map[string]any{"status": "blocked", "goal": "goal", "currently_doing": "doing", "why_it_matters": "matters", "completed_so_far": []string{"done"}, "next_step": "next", "owner_action_required": nil}, "technical_evidence": reportEvidence, "published_by": "delivery", "published_at": now}
	plannerReportStatus := map[string]any{"schema_version": 1, "id": "report", "project_id": "project", "handoff_id": "handoff", "task_id": task.ID, "run_id": run.ID, "report_type": "blocked", "owner_summary": plannerReport["owner_summary"], "published_by": "delivery", "published_at": now, "status": "published"}
	plannerReportState := map[string]any{"schema_version": 1, "report_id": "report", "report_sha256": strings.Repeat("b", 64), "status": "resolved", "updated_at": now}
	revisionSample := map[string]any{"schema_version": 1, "id": "GTW-TSK1.REV1", "task_id": "GTW-TSK1", "task_revision": 1, "revision_sha256": strings.Repeat("e", 64), "project_id": "project", "title": "title", "objective": "objective", "branch": "feature/x", "base_revision": strings.Repeat("b", 40), "acceptance_criteria": []string{"criterion"}, "constraints": []string{}, "status": "created", "created_by": "delivery", "created_at": now}
	revisionStatusSample := map[string]any{"schema_version": 1, "id": "GTW-TSK1.REV1", "task_id": "GTW-TSK1", "task_revision": 1, "revision_sha256": strings.Repeat("e", 64), "status": "created", "branch": "feature/x", "base_revision": strings.Repeat("b", 40), "created_at": now}

	f.handoffSummary = handoffSummary
	f.handoff = handoff
	f.handoffStatus = handoffStatus
	f.reportEvidence = reportEvidence
	f.plannerReport = plannerReport
	f.plannerReportStatus = plannerReportStatus
	f.plannerReportState = plannerReportState
	f.revisionSample = revisionSample
	f.revisionStatusSample = revisionStatusSample
}
