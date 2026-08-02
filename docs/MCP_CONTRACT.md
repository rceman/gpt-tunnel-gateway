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

`tools/call.params` accepts the optional protocol `_meta` object up to 64 KiB. All other unknown envelope fields and all unknown tool arguments remain rejected.

Remote tools mirror typed CLI operations except `run_finalize`, which remains local-agent-only. No generic shell, generic Git, arbitrary path, or unrestricted file tool exists.

The v0.6.0 direct project-session tools are:

- `agent_send(project_id, message)`: one bounded, serialized Airelay prompt;
- `agent_tail(project_id, lines=4, skip=0)`: one bounded read window;
- `agent_status(project_id)`: normalized waiting/running/idle/error state and
  capacity warnings.

They resolve only configured registered projects and never accept a caller
session key. They do not create or mutate durable task/run/plan state or Git.

The normal run surface contains `run_read`, `run_report`, and
`run_review_snapshot`; there is no `run_evidence` operation. New run records
expose only `completion_path`. Protocol-v1 run records may appear in bounded
run history with legacy paths redacted, but their reports are history-only and
cannot be finalized or converted into workflow-2.0 reports.

Plan storage uses schema-v2 sectional records. The plan tools are `plan_read`, `plan_cutover`, `plan_update`, `plan_section_read`, `plan_section_create`, `plan_section_update`, `plan_section_delete`, and `plan_render`. `plan_read` returns only the bounded manifest and section index; full descriptions require an exact section read or explicit render. Schema-v1 conversion is an owner-invoked one-time `plan_cutover`; ordinary reads never trigger it. Manifest updates are partial. Section mutations use independent optimistic `expected_section_revision` values, while Git history retains deleted records.

Git tools are read-only relative to source and remotes. `git_refresh` updates only a managed bare mirror.
