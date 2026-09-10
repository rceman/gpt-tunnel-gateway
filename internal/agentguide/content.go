package agentguide

// Content is the canonical zero-state supervision guide shared by CLI and MCP.
type Content struct {
	RoleAuthority    string `json:"role_authority"`
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
		RoleAuthority:    "Planner owns Task/ADR decisions, hotfix lanes, integration, and activation. The Agent implements only in the assigned worktree; never create a lane, edit main, merge, rebase, reset, or cherry-pick.",
		Startup:          "Use the exact Planner-assigned worktree. Run one compound preflight: pwd; git branch --show-current; git rev-parse HEAD; git status --short. If path, branch, base, or cleanliness differs, report and stop. Do not perform worktree, branch, history, or remote archaeology unless the supplied path is invalid.",
		CanonicalState:   "Use supported gpt-tunnel CLI/actions or Planner-supplied Task, ADR, and project state. Never scan ~/.local/share/gpt-tunnel-gateway, Hub/SQLite, or unrelated home directories. Do not invent commands; the zero-state command is exactly gpt-tunnel guide.",
		Exploration:      "Start with targeted repo-local reads. Prefer rg for source search when it is installed; if it is absent, use a bounded equivalent such as repo-local grep, find, or sed. Agent-native bounded read/search tools are also valid. Batch compatible checks, reuse one captured status/branch/HEAD result, and avoid repeated ls, find, status, or log scans. Keep all exploration inside the assigned repository/worktree; tool absence never broadens scope.",
		StopFast:         "If expected source, tests, dependency, authority, or canonical command is missing or ambiguous, report the exact path/command/error and stop in roughly 2-3 commands. Do not guess, broaden scope, or invent a workaround.",
		Checkpoints:      "Production first: make one immutable checkpoint and hand off exact SHA, parent, paths, gates, and clean status for review. Only when instructed, make a tests-only child checkpoint. Never amend, rewrite history, or mix production and test changes.",
		Testing:          "For ADR118 rev2, test the cheapest deterministic layer first, retain a small real-boundary contract set, and use deterministic fakes or mocks for bulk service behavior. Preserve assertions, fail-closed semantics, and explicit boundary coverage.",
		ExecutionExample: "GOOD reposuite README-only proof: (1) run the compound assigned-worktree preflight; (2) run find . -maxdepth 2 -type f -name 'README*' -print, then inspect the listed README with bounded sed -n '1,200p'; (3) report the exact result. BAD: git log --all, git ls-remote, remote enumeration, ~/.local/share/gpt-tunnel-gateway scans, or long TODO archaeology before the supplied repo is understood.",
		CLIUsage:         "Zero-state: gpt-tunnel guide. Safe reads: gpt-tunnel project list; gpt-tunnel project read <project_id>; gpt-tunnel project status <project_id>; gpt-tunnel plan read <project_id>; gpt-tunnel plan render <project_id>; gpt-tunnel adr list <project_id>; gpt-tunnel adr read <project_id> <adr_id>; before work, use gpt-tunnel task read <key> through the running Gateway session authority. The CLI has no Task list surface: use Planner-supplied Task state or the canonical Gateway action, never filesystem archaeology. gpt-tunnel task work/finalize are execution mutations; invoke only when Planner explicitly instructs. Do not use project update/register, ADR create, plan update, or agent register as an ordinary coding Agent.",
		Architecture:     "The Planner role may have multiple durable Planner sessions; the project has exactly one attached enabled coding Agent. Train and watcher state are not Agent supervision authority.",
		Tail:             "agent/tail accepts {session?,lines?}. Omitted session selects the unique active durable Agent session; zero is an error and more than one requires explicit SA-*. Examples: {} or {lines:30}; explicit: {session:\"SA-GTW-AB12\",lines:30}. The selected durable session resolves internally to its stored Airelay ref.",
		StatusAwait:      "agent/status and agent/await accept an optional logical Agent selector; when omitted, they use the server-selected attached coding Agent. They do not accept a durable SA-* selector.",
		PromptInterrupt:  "agent/prompt and agent/interrupt accept an optional logical Agent selector; when omitted, they use the server-selected attached coding Agent. Prompt sends a bounded message; interrupt cancels the current turn and may submit a bounded replacement. Neither accepts a durable SA-* selector.",
		Authority:        "Gateway schemas and handlers are the operational contract. No repo guide file, Train record, watcher, project Airelay-session substitution, or duplicate policy source is authoritative.",
	}
}
