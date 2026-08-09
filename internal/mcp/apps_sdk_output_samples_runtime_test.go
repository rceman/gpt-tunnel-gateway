package mcp

import (
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (f canonicalOutputFixture) canonicalRuntimeOutputSamples() map[string]any {
	publicRun := f.publicRun.(service.PublicRun)
	ref := f.ref.(gitx.Ref)
	commit := f.commit.(gitx.Commit)
	worktree := f.worktree.(gitx.WorktreeStatus)
	return map[string]any{
		"run_list": map[string]any{"runs": []service.PublicRun{publicRun}}, "run_read": publicRun, "run_status": publicRun, "run_report": f.report,
		"run_review_snapshot": f.snapshot,
		"run_agent_tail":      map[string]any{"text": "tail text"},
		"run_resume":          service.RunResumeResult{RunID: "run", CompactionEventID: "event", State: "compacted_resuming", Sent: true, ExitCode: 0, ControllerReachable: true, MessageDigest: strings.Repeat("a", 64)},
		"agent_send":          service.AgentSendResult{ProjectID: "project", Delivered: true, ExitCode: 0, Stdout: "delivered", Stderr: "", StartedAt: f.now, FinishedAt: f.now},
		"agent_tail":          service.AgentTailResult{ProjectID: "project", Text: "tail text", Lines: 4, Skip: 0},
		"agent_status":        service.AgentStatusResult{ProjectID: "project", State: "running", ControllerReachable: true, CapacityWarnings: []string{}, ExitCode: 0},
		"operator_record":     map[string]any{"event": f.operatorEvent, "operation": f.operation}, "operator_history": f.operatorHistory, "operator_checkpoint": map[string]any{"event": f.operatorEvent, "operation": f.operation},
		"run_sweep": service.SweepResult{Checked: 1, Items: []service.SweepItem{{RunID: "run", Action: "reprompt", Status: "awaiting_result"}}}, "run_cancel": f.operation, "run_cancel_acknowledge_no_mutation": f.operation,
		"git_refresh": map[string]any{"project_id": "project", "refreshed": true}, "git_refs": map[string]any{"refs": []gitx.Ref{ref}},
		"git_log": map[string]any{"commits": []gitx.Commit{commit}}, "git_show": map[string]any{"text": "show"}, "git_tree": map[string]any{"paths": []string{"README.md"}},
		"git_read_file": map[string]any{"path": "README.md", "revision": "main", "content": "content"}, "git_diff": map[string]any{"diff": "diff"},
		"git_compare": f.compare, "git_merge_base": map[string]any{"merge_base": worktree.Head}, "git_worktree_status": worktree,
		"git_worktree_diff": map[string]any{"diff": "diff", "staged": false},
	}
}
