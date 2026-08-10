# MCP contract

The daemon implements JSON-RPC Streamable HTTP at `/mcp` and supports:

- `initialize`;
- `ping`;
- `tools/list`;
- `tools/call`.

Every tool descriptor declares an exact object-rooted `outputSchema` and explicit `readOnlyHint`, `destructiveHint`, `idempotentHint`, and `openWorldHint` annotations. Successful results include object-shaped `structuredContent` that is validated against the declared schema before it is returned. Collections are wrapped in named fields such as `projects`, `tasks`, `runs`, `refs`, or `commits`. Tool failures set `isError: true` and omit `structuredContent`.

Registration, the input/output schema registries, annotations, and contract
tests share the canonical tool manifest in `internal/mcp/schema.go`. The
manifest is checked for exact registration parity at runtime. Smoke tests derive
their assertions from tool names and schemas; they do not assert a frozen tool
count. `plan_update` advertises the workflow-v2 sectional fields only and never
advertises the obsolete `body` field.

Project workflow policy is a durable, revisioned Hub record. The
`project_workflow_policy_read`, `project_workflow_policy_adopt`, and
`project_workflow_policy_update` tools expose it to Planner/Delivery with
optimistic Hub revision guards. `project_status`, `task_read`, and active task
packets include the policy projection. Task creation and supersession require
an explicit operation class; the gateway derives the effective CI field and
mode from the policy. `activation` is explicitly disabled, non-blocking and
never inherits task-merge CI. `disabled` and `observe` never become a hosted-CI
wait request, and missing or invalid policy is a visible blocking error.
Policy mutation authority comes only from a trusted server-owned MCP
connection/session/capability context outside serialized arguments. The current
transport has no such authority and therefore returns stable
`AUTHORITY_UNAVAILABLE` before policy mutation. `updated_by` is provenance and
`expected_hub_revision` is concurrency control; neither grants authority.
Even trusted policy mutation rejects any relevant active operational Run before
Hub mutation and rechecks the durable Run snapshot inside the transaction.
Policy reads remain available without write authorization.

`tools/call.params` accepts the optional protocol `_meta` object up to 64 KiB. All other unknown envelope fields and all unknown tool arguments remain rejected.

Remote tools mirror typed CLI operations except `run_finalize`, which remains local-agent-only. No generic shell, generic Git, arbitrary path, or unrestricted file tool exists.

The v0.6.0 direct project-session tools are:

- `agent_send(project_id, message)`: one bounded, serialized Airelay prompt;
- `agent_tail(project_id, lines=4, skip=0, cursor?)`: one bounded initial
  window or incremental delta;
- `agent_status(project_id)`: normalized bounded liveness state and capacity
  and rate-limit warnings.

They resolve only configured registered projects and never accept a caller
session key. They do not create or mutate durable task/run/plan state or Git.
Tail responses always include an opaque `next_cursor` and `has_more`. An
initial call without a cursor returns the newest bounded window. A later call
with that cursor returns only output observed after the cursor's snapshot,
including a successful empty delta when nothing new is available. `skip` is
legacy initial-window behavior and cannot be combined with a cursor. Cursors
are bound to the project/session scope and retained snapshot; malformed,
cross-scope, replaced-session, truncated, or otherwise unmatchable cursors
are rejected as stale/invalid and require a new initial read. `run_agent_tail`
uses the same cursor semantics for the current run session.
`agent_send` is emergency/control-plane communication only; it never grants
new task scope or merge, release, or deployment authorization. Messages such as
“implement the next feature”, “merge and release this branch”, “deploy this”,
or “continue the roadmap” are misuse and must use an explicitly authorized
durable workflow instead.

`project_status(project_id)` is the canonical aggregated progress snapshot and
includes the latest task/run, normalized liveness state, bounded tail,
activity age, blocker classification, recommended action, and safe component
error codes. Healthy status, tail, repository and bounded hub reads are
collected concurrently within one bounded request; a component failure does
not expose raw command output or discard the other components. The new
`run_resume(run_id)` operation is the only canonical compaction recovery write;
it accepts no caller message or session key and is one-shot per compaction
event. `run_sweep` may perform the same bounded recovery only after all safety
checks pass. Routine project status omits the configured repository root,
mirror, Airelay session key, gateway state path, and completion path. Those
execution details remain bounded and are not completion destinations. The
active Agent packet requires the canonical `gpt-tunnel run write-completion
<RUN-ID> --completion-file <INPUT>` operation; the Gateway derives the
completion destination internally.

`task_list` is the bounded task backlog surface. It accepts an optional
case-insensitive `query` over task identity and human-readable metadata
(including ID, derived slug, branch, title, objective, status, creator, and
criteria), an exact workflow `status` filter, a server-enforced `limit`, and
an opaque `cursor`. Results are newest-first by `created_at` with task-ID
tie-breaking, and return `has_more` plus `next_cursor` for continuation.
The default and hard maximum are both ten results per public page and are
enforced by the Gateway; callers must not retrieve or scan an unbounded
backlog client-side.

All other growing public collections use the shared bounded-page contract:
`limit` defaults to 20 and has a hard maximum of 100 (also capped by the
configured `max_list_items`), and `cursor` is an opaque deterministic
continuation token. `project_list`, `run_list`, `adr_list`,
`task_revision_list`, `plan_history`, `git_refs`, `git_log`, `git_tree`,
`delivery_handoff_list`, and `planner_report_list` return `next_cursor` and
`has_more`; their ordering is stable for the requested snapshot. Cursors for
`git_log` and `git_tree` are bound to the exact managed mirror, revision, and
tree path scope; a missing or cross-scope cursor is rejected.

The audit of public read-many actions is:

| Action family | Bound | Continuation |
| --- | --- | --- |
| `task_list` | default/max 10 | opaque cursor |
| `project_list`, `run_list`, `adr_list`, `git_refs`, `git_tree` | default 20/max 100 | opaque cursor |
| `git_log`, `plan_history`, `task_revision_list`, `delivery_handoff_list`, `planner_report_list` | default 20/max 100 | opaque cursor |
| `operator_history` | existing bounded service limit | existing `after` continuation |

Singleton reads such as `project_read`, `task_read`, `run_read`, `adr_read`,
`plan_read`, and `git_read_file` do not accept collection limits.

The normal run surface contains `run_read`, `run_report`, and
`run_review_snapshot`; there is no `run_evidence` operation. Routine run list,
read, and status projections omit gateway-internal completion paths. The
active task execution packet does not expose a caller-actionable
`completion_path`; finalization reads only the exact Run-specific path derived
from configured StateDir and the canonical Run ID.
Protocol-v1 run records may appear in bounded run history with legacy paths
redacted, but their reports are history-only and cannot be finalized or
converted into workflow-2.0 reports.

Plan storage uses schema-v2 sectional records. The plan tools are `plan_read`, `plan_cutover`, `plan_update`, `plan_section_read`, `plan_section_create`, `plan_section_update`, `plan_section_delete`, and `plan_render`. `plan_read` returns only the bounded manifest and section index; full descriptions require an exact section read or explicit render. Schema-v1 conversion is an owner-invoked one-time `plan_cutover`; ordinary reads never trigger it. Manifest updates are partial. Section mutations use independent optimistic `expected_section_revision` values, while Git history retains deleted records.

Git tools are read-only relative to source and remotes. `git_refresh` updates only a managed bare mirror.
