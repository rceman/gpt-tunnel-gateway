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
		RoleAuthority:    "Planner owns WHAT/WHY, architecture, durable semantics, ADR/Task/RULE plus Milestone/Track composition and scope, acceptance, dependencies/priority, final Track semantic review, and executable-work curation; it is not the dispatcher/supervisor/review/test/integrate proxy. Lead owns HOW: dispatch, Worker supervision, technical review/rework, verification, integration, continuation and lifecycle decisions, but never mutates Planner semantics, never creates or updates Planner-owned Tasks, Tracks, ADRs, or Rules, and never hand-mutates lanes or canonical source via shell Git; canonical Task actions own mechanics. Worker owns implementation/testing and submits one production+tests candidate only via CLI gpt-tunnel task submit-code|submit-rebase, never native MCP.",
		Delegation:       "Milestone Track is the Planner-to-Lead delegation unit, not an Agent/Worker/ad-hoc queue and not a Wave. Track order is planning intent, not FIFO: Lead weighs membership, dependencies, priority, status, and Worker availability when choosing eligible Tasks. Any multiple Workers are assigned at dispatch time. Only the durable assigned Worker owns the lane and the submit CLI; a sidekick or advisor is advisory only and holds no lane or Task authority. No task/queue/Agent queue, Planner/Worker impersonation, or alternate-role bypass. Lead never proxies a Worker submit or impersonates a Session. ADR138 role-permissive runtime does not transfer semantic authority between PLAW roles or make Worker-owned submit actions Lead-owned.",
		Startup:          "Use the exact Planner-assigned worktree. Run one compound preflight: pwd; git branch --show-current; git rev-parse HEAD; git status --short. If path, branch, base, or cleanliness differs, report and stop. Do not perform worktree, branch, history, or remote archaeology unless the supplied path is invalid.",
		CanonicalState:   "Use supported gpt-tunnel CLI/actions or Planner-supplied Task, ADR, and project state. Never scan ~/.local/share/gpt-tunnel-gateway, Hub/SQLite, or unrelated home directories. Do not invent commands; the zero-state command is exactly gpt-tunnel guide.",
		Exploration:      "Start with targeted repo-local reads. Prefer rg for source search when it is installed; if it is absent, use a bounded equivalent such as repo-local grep, find, or sed. Agent-native bounded read/search tools are also valid. Batch compatible checks, reuse one captured status/branch/HEAD result, and avoid repeated ls, find, status, or log scans. Keep all exploration inside the assigned repository/worktree; tool absence never broadens scope.",
		StopFast:         "If expected source, tests, dependency, authority, or canonical command is missing or ambiguous, report the exact path/command/error and stop in roughly 2-3 commands. Do not guess, broaden scope, or invent a workaround.",
		Checkpoints:      "Make one immutable checkpoint containing production and focused test changes; hand off exact SHA, parent, paths, gates, and clean status for review. There is no ordinary tests-only submit hop. Never amend or rewrite history. Lead may run authorized non-final staging, disposable E2E, or preflight, including focused post-Task integration checks after risky Tasks, subsets, or Track end; final project activate/release waits for source-bound Planner Track review. Never create a lane, edit main, merge, rebase, reset, or cherry-pick: that is coding Agent/Worker lane discipline, not Lead integration.",
		Testing:          "Before submit-code, Worker runs only focused/affected deterministic tests plus cache-aware scripts/test-fast.py; committed changes use origin/main (or main), GPT_TEST_BASE/--base overrides it, and the lane never uses -count=1. Do not run go test ./..., scripts/test-full.sh, race, performance, profile, or live E2E. After submit-code, stop for Lead review; Lead owns task/test full verification and receipt reuse for the exact unchanged candidate. Start with the cheapest layer and use deterministic fakes or mocks for bulk service behavior. Preserve assertions, fail-closed behavior, and boundary coverage. Keep diagnostics bounded and retries explicit and bounded.",
		ExecutionExample: "GOOD reposuite README-only proof: (1) run the compound assigned-worktree preflight; (2) run find . -maxdepth 2 -type f -name 'README*' -print, then inspect the listed README with bounded sed -n '1,200p'; (3) report the exact result. BAD: git log --all, git ls-remote, remote enumeration, ~/.local/share/gpt-tunnel-gateway scans, or long TODO archaeology before the supplied repo is understood.",
		CLIUsage:         "Zero-state: gpt-tunnel guide. From a registered repository cwd, get the Planner token with gpt-tunnel project token using GPT_TUNNEL_ADMIN_SESSION; MCP never mints it; list/read/guide/logs never echo it. Public session_start accepts only {token}; server resolves Gateway, project, role, and optional Agent. Reads: gpt-tunnel project list/read/status; plan read/render; adr list/read; before work, use gpt-tunnel task read <key>. Project discovery is project-scoped; Admin sessions are excluded. The CLI has no Task list surface: use Planner state or Gateway. task work/finalize are execution mutations. Worker submits through fixed CLI only. Ordinary coding Agents do not use project update/register, ADR create, plan update, or agent register.",
		Architecture:     "The project uses durable Planner, Lead, Advisor, and Worker role Sessions. Agent is a logical project identity and may be referenced by several roles only through distinct role-bound Sessions. Train and watcher state are not workflow-role authority.",
		Tail:             "agent/tail accepts {agent?,lines?}. Omitted agent uses the server-selected logical Agent. Callers cannot select a durable Session or an internal control reference.",
		StatusAwait:      "agent/status and agent/await supervise the server-selected logical Agent. For task/test, task/integrate, or other server mutations, use operation/read and bounded operation/await; never use agent/await or shell sleep for server-operation waiting.",
		PromptInterrupt:  "agent/prompt and agent/interrupt accept a logical Agent selector. Prompt sends a bounded message; interrupt cancels the current turn and may submit a bounded replacement. Neither accepts a caller-selected Session or internal control reference.",
		Authority:        "Gateway schemas and handlers are the operational contract. No repo guide file, Train record, watcher, or duplicate policy source is authoritative. ADR72 Gates 1-20 remain the sole gate taxonomy, including the Gates 9/12/14/19/20 public-response evidence requirements; there is no parallel gate taxonomy. Friction, lesson, and decision evidence goes through canonical journal/* actions; journal/contract is the sole stream-rules authority. No direct Lead-to-Planner channel exists; owner/operator relay is only for semantic blockers and completed Track handoff, not execution proxy. Final project activate/release waits for source-bound Planner Track review; bounded ADR138 debug break-glass is only approved recovery.",
	}
}
