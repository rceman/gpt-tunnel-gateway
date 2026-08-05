# MCP tool reference

The authoritative tool inventory is the canonical manifest in
`internal/mcp/schema.go`. The daemon validates registration, output schemas,
and annotations against that manifest at startup of the tool registry. Use
`tools/list` for the live surface; do not infer a contract from a fixed count.

Every descriptor has:

- an object-rooted `inputSchema` with `additionalProperties: false`;
- an object-rooted `outputSchema`;
- all four explicit annotations;
- a typed handler whose successful structured output is validated before it is
  returned.

`tools/call` accepts `name`, `arguments`, and an optional bounded object `_meta`.
Unknown envelope fields, unknown tool arguments, and the obsolete workflow-v1
`body` plan field are rejected. `run_agent_tail` returns structured `{text}`
output. The v0.6.0 direct project-session tools are `agent_send`, `agent_tail`,
and `agent_status`; they do not create durable workflow or Git state. `agent_send`
accepts only `project_id` and `message`, `agent_tail` defaults to four lines and
supports a bounded `skip`, and `agent_status` returns normalized state plus
capacity warnings. Session keys are never caller-supplied.

`project_status` is the single-call progress snapshot. Healthy bounded status,
tail, repository and hub components are collected concurrently; partial
failures are represented by sanitized `component_errors`. `run_resume` accepts
only `run_id` and performs one gateway-generated context-compaction recovery
after validating ownership, active-run uniqueness, compaction evidence,
unanswered questions, and repository conflict state.

The G1 task lifecycle tools are `task_mark_merge_ready`, `task_defer`, and
`task_mark_merged`. They mutate durable task lifecycle records only and never
perform repository branch operations. `task_mark_merge_ready` records the
exact repository HEAD from the latest canonical successful report;
`task_defer` records a bounded reason while preserving the reviewed HEAD; and
`task_mark_merged` verifies that the reviewed task HEAD is already contained
in the exact remote `develop` HEAD and records that receipt. The actual merge
is a later integration operation; `task_mark_merged` does not merge, push,
checkout, or delete branches.

## Operator journal bootstrap

The immutable bootstrap tools are `operator_record`, `operator_history`, and
`operator_checkpoint`. They store concise structured context under each
adopted project's journal. `operator_record` accepts only
`user_talk`, `reasoning_summary`, `task_plan`, `task_review`, and `correction`;
reserved `operation` and `checkpoint` kinds are not caller-created through
that endpoint. History is numeric by event number, supports an exclusive
project-scoped cursor and exact kind filtering, and preserves correction
links. Prompts, hidden reasoning, secrets, paths, and unbounded logs are not
accepted.

## Project compact identifiers

Read the current allocation record with:

```text
gpt-tunnel project identifiers-read <project-id>
```

Adopt a three-letter uppercase code atomically with:

```text
gpt-tunnel project identifiers-adopt <project-id> <PROJECT_CODE> [--expected-hub-revision <sha>]
```

Examples include `GRP` for `gpt-review-planner` and `GTW` for
`gpt-tunnel-gateway`. The MCP equivalents are `project_identifiers_read` with
`{"project_id":"gpt-tunnel-gateway"}` and
`project_identifiers_adopt` with `project_id`, `project_code`, and optional
`expected_hub_revision`.

Adoption is one optimistic-revision-guarded hub transaction. It creates one
immutable allocation record, rejects an existing record or duplicate project
code, and initializes task and ADR counters. The counters and adopted code
are not replaced by a later adoption. This slice exposes the record only;
task, ADR, and run creation still use their existing UUID paths and have not
switched to compact identifiers.

## Cancellation acknowledgement

The CLI command

```text
gpt-tunnel run cancel-acknowledge-no-mutation <run-id> [--expected-hub-revision <sha>]
```

and the MCP tool `run_cancel_acknowledge_no_mutation` expose the same existing
service operation. The MCP input is a closed object containing required
`run_id` and optional `expected_hub_revision`; its output is the canonical
operation result. The operation acknowledges an already delivered cooperative
cancellation and terminalizes it only when the configured task worktree is
clean at the task's immutable base revision. It does not send a cancellation,
hard-interrupt Airelay, or mutate the task's implementation.

`run_cancel` remains the cooperative cancellation request through Airelay.
Neither operation is a hard interrupt. A dirty worktree, committed changes,
an ambiguous repository identity, failed or incomplete cancellation delivery,
or any other failed proof blocks terminalization. The acknowledgement command
rejects missing, duplicate, unknown, or extra arguments and supports the
optimistic hub revision guard.
