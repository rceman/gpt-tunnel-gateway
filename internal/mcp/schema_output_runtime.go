package mcp

func runResumeOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"run_id": outputString(), "compaction_event_id": outputString(), "state": outputEnum("compacted_resuming", "error"),
		"sent": outputBoolean(), "exit_code": outputInteger(), "controller_reachable": outputBoolean(), "message_digest": outputString(), "error": outputString(),
	}, "run_id", "compaction_event_id", "state", "sent", "exit_code", "controller_reachable", "message_digest")
}

func reviewSnapshotOutputSchema() map[string]any {
	timeField := outputDateTime()
	run := closedOutput(map[string]any{"id": outputString(), "task_id": outputString(), "project_id": outputString(), "status": outputString(), "branch": outputString(), "base_revision": outputString(), "created_at": timeField, "dispatched_at": timeField, "finished_at": timeField}, "id", "task_id", "project_id", "status", "branch", "base_revision", "created_at")
	task := closedOutput(map[string]any{"id": outputString(), "sha256": outputString(), "title": outputString(), "objective": outputString(), "branch": outputString(), "base_revision": outputString(), "acceptance_criteria": outputArray(outputString()), "constraints": outputArray(outputString()), "required_gates": outputArray(outputString()), "created_by": outputString(), "created_at": timeField, "task_state_status": outputString()}, "id", "sha256", "title", "objective", "branch", "base_revision", "acceptance_criteria", "constraints", "required_gates", "created_by", "created_at", "task_state_status")
	gate := completionGateResultAnyIDOutputSchema()
	report := closedOutput(map[string]any{"available": outputBoolean(), "error": outputString(), "status": outputString(), "summary": outputString(), "repository_head": outputString(), "repository_branch": outputString(), "repository_clean": outputBoolean(), "commits": outputArray(outputString()), "changed_files": outputArray(outputString()), "gate_results": outputArray(gate), "server_gate_results": outputArray(gate), "acceptance_coverage": outputArray(outputString()), "deviations": outputArray(outputString()), "remaining_risks": outputArray(outputString()), "finished_at": timeField, "hub_commit": outputString()}, "available")
	evidence := closedOutput(map[string]any{"available": outputBoolean(), "error": outputString(), "head": outputString(), "branch": outputString(), "worktree_clean": outputBoolean(), "notes": outputArray(outputString()), "recorded_at": timeField}, "available")
	worktree := closedOutput(map[string]any{"branch": outputString(), "head": outputString(), "upstream": outputString(), "ahead": outputInteger(), "behind": outputInteger(), "clean": outputBoolean()}, "branch", "head", "ahead", "behind", "clean")
	compare := closedOutput(map[string]any{"merge_base": outputString(), "left_only": outputInteger(), "right_only": outputInteger(), "error": outputString()}, "left_only", "right_only")
	repo := closedOutput(map[string]any{"refresh_attempted": outputBoolean(), "refresh_succeeded": outputBoolean(), "refresh_error": outputString(), "default_branch": outputString(), "default_head": outputString(), "default_head_error": outputString(), "task_branch": outputString(), "task_branch_published": outputBoolean(), "task_branch_head": outputString(), "task_branch_error": outputString(), "worktree": worktree, "worktree_error": outputString(), "evidence_head_reachable": outputBoolean(), "evidence_head_error": outputString(), "base_to_evidence": compare, "default_to_evidence": compare, "changed_files": outputArray(outputString()), "changed_files_error": outputString(), "diff_stat": outputString(), "diff_stat_error": outputString()}, "refresh_attempted", "refresh_succeeded", "default_branch", "task_branch", "task_branch_published", "worktree", "evidence_head_reachable", "base_to_evidence", "default_to_evidence", "changed_files")
	check := closedOutput(map[string]any{"id": outputString(), "severity": outputString(), "status": outputString(), "detail": outputString()}, "id", "severity", "status", "detail")
	return closedOutput(map[string]any{"schema_version": outputInteger(), "run": run, "task": task, "report": report, "evidence": evidence, "repository": repo, "checks": outputArray(check), "review_state": outputString(), "next_action": outputString()}, "schema_version", "run", "task", "report", "evidence", "repository", "checks", "review_state", "next_action")
}

func projectConfigOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"remote": outputString(), "default_branch": outputString(),
	}, "remote", "default_branch")
}
