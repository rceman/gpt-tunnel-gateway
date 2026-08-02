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
