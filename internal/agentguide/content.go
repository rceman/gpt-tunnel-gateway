package agentguide

// Content is the canonical zero-state supervision guide shared by CLI and MCP.
type Content struct {
	RoleAuthority    string `json:"role_authority"`
	Delegation       string `json:"delegation"`
	Startup          string `json:"startup"`
	CanonicalState   string `json:"canonical_state"`
	Exploration      string `json:"exploration_budget"`
	StopFast         string `json:"stop_fast"`
	Checkpoints      string `json:"checkpoints"`
	Testing          string `json:"testing"`
	ExecutionExample string `json:"execution_example"`
	CLIUsage         string `json:"cli_usage"`
	Architecture     string `json:"architecture"`
	Tail             string `json:"tail"`
	StatusAwait      string `json:"status_await"`
	PromptInterrupt  string `json:"prompt_interrupt"`
	Authority        string `json:"authority"`
}

func Canonical() Content {
	return Content{
		RoleAuthority:    "The active durable Session fixes the role. Planner owns durable WHAT/WHY: architecture, ADR/Task/Rule and Milestone/Track composition, scope, acceptance, dependencies and priority, final Track review, and executable-work curation. Planner is not the dispatcher, Worker supervisor, technical reviewer, tester, or integration proxy. Lead owns dispatch, Worker supervision, technical review/rework, verification, integration, continuation, Track submission, and Task lifecycle mechanics, but never mutates Planner-owned semantics. Worker implements assigned Tasks and makes one production+tests candidate handoff through gpt-tunnel task submit-code.",
		Delegation:       "Planner delegates one Track through durable MSG carrying only its key. Ordered membership is planning intent, not FIFO. Lead rereads membership and live dependency, priority, status, execution stage, and Worker availability; selects eligible members sequentially, reuses persistent execution after restart, never dispatches while Worker has an actionable Task, and continues without ordinary Planner round-trips. MSG is only for genuine semantic blockers and completed Track handoff.",
		Startup:          "Use the exact assigned worktree. Check path, branch, base, and cleanliness before editing; report and stop if any differ.",
		CanonicalState:   "Use supported gpt-tunnel CLI/actions or Planner-supplied Task, ADR, Rule, and project state. Never scan Gateway Hub/SQLite or unrelated home directories. Do not invent commands; the zero-state command is exactly gpt-tunnel guide.",
		Exploration:      "Start with targeted repository-local reads and bounded searches. Batch compatible checks, reuse one captured status, base, and revision result, and avoid repeated broad scans. Keep exploration inside the assigned repository/worktree; tool absence never broadens scope.",
		StopFast:         "If expected source, tests, dependency, authority, or canonical command is missing or ambiguous, report the exact path, command, or error and stop. Do not guess, broaden scope, or invent a workaround.",
		Checkpoints:      "Keep one immutable production+tests candidate with focused evidence. Use deterministic fakes or mocks, bounded diagnostics, and explicit bounded retries. Worker hands off once through the fixed CLI; Lead owns review and verification.",
		Testing:          "Before submit-code, Worker runs only focused/affected deterministic tests plus scripts/test-fast.py. Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E. Lead owns project-required full Task verification after submission.",
		ExecutionExample: "Read the assigned Task, inspect its named source and tests, make the smallest relevant change, run focused/affected checks plus the fast profile, submit one candidate, and report exact evidence.",
		CLIUsage:         "Zero-state: gpt-tunnel guide. After session_start, use authenticated project-bound actions and read the project-bound agent/guide and task/guide before workflow work. Reads: gpt-tunnel project list/read/status; plan read/render; adr list/read; before work, use gpt-tunnel task read <key>. Workers submit once through the fixed CLI; ordinary Agents do not edit project settings or create Planner-owned records.",
		Architecture:     "Planner, Lead, Advisor, and Worker are durable project Session roles. A logical Agent identity does not replace Session authority. Advisors hold no execution lane. No direct Lead-to-Planner channel exists.",
		Tail:             "agent/tail accepts {agent?,lines?}; when omitted, the server uses the project-bound Agent selection. Callers cannot select a durable Session.",
		StatusAwait:      "After restart, reread canonical Message, Track, Task, and Operation state; reconstruct eligibility from live projections, reuse existing Worker execution, and use bounded await actions rather than polling. Send a durable MSG only when a genuine semantic blocker or completed Track handoff requires it.",
		PromptInterrupt:  "Lead uses agent/prompt and bounded status/await actions to supervise the assigned Worker. Prompts do not alter Planner-owned semantics; Planner delegates Track keys through durable MSG, not direct execution prompts. MSG is only for genuine blockers and completed Track handoff.",
		Authority:        "Gateway action schemas and accepted project Rules govern workflow. ADR72 Gates 1-20 are the sole review taxonomy, and journal/contract is the sole journal stream-rules authority. Final project activation/release waits for source-bound Planner Track review.",
	}
}
