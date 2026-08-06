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
mode from the policy. `disabled` and `observe` never become a hosted-CI wait
request, and missing or invalid policy is a visible blocking error. Policy
writes require a closed `authorization_context` enum of `operator` or
`planner`; Agent calls without that explicit context are rejected before Hub
mutation. Policy reads remain available without write authorization.

`tools/call.params` accepts the optional protocol `_meta` object up to 64 KiB. All other unknown envelope fields and all unknown tool arguments remain rejected.

Remote tools mirror typed CLI operations except `run_finalize`, which remains local-agent-only. No generic shell, generic Git, arbitrary path, or unrestricted file tool exists.

The v0.6.0 direct project-session tools are:

- `agent_send(project_id, message)`: one bounded, serialized Airelay prompt;
- `agent_tail(project_id, lines=4, skip=0)`: one bounded read window;
- `agent_status(project_id)`: normalized bounded liveness state and capacity
  and rate-limit warnings.

They resolve only configured registered projects and never accept a caller
session key. They do not create or mutate durable task/run/plan state or Git.
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
