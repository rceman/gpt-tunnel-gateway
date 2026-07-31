# MCP contract

The daemon implements JSON-RPC Streamable HTTP at `/mcp` and supports:

- `initialize`;
- `ping`;
- `tools/list`;
- `tools/call`.

Every tool descriptor declares an exact object-rooted `outputSchema` and explicit `readOnlyHint`, `destructiveHint`, `idempotentHint`, and `openWorldHint` annotations. Successful results include object-shaped `structuredContent` that is validated against the declared schema before it is returned. Collections are wrapped in named fields such as `projects`, `tasks`, `runs`, `refs`, or `commits`. Tool failures set `isError: true` and omit `structuredContent`.

`tools/call.params` accepts the optional protocol `_meta` object up to 64 KiB. All other unknown envelope fields and all unknown tool arguments remain rejected.

Remote tools mirror typed CLI operations except `run_finalize`, which remains local-agent-only. No generic shell, generic Git, arbitrary path, or unrestricted file tool exists.

Plan storage uses schema-v2 sectional records. The plan tools are `plan_read`, `plan_cutover`, `plan_update`, `plan_section_read`, `plan_section_create`, `plan_section_update`, `plan_section_delete`, and `plan_render`. `plan_read` returns only the bounded manifest and section index; full descriptions require an exact section read or explicit render. Schema-v1 conversion is an owner-invoked one-time `plan_cutover`; ordinary reads never trigger it. Manifest updates are partial. Section mutations use independent optimistic `expected_section_revision` values, while Git history retains deleted records.

Git tools are read-only relative to source and remotes. `git_refresh` updates only a managed bare mirror.
