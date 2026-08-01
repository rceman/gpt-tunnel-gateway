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
output. Direct project-session tools are reserved for the v0.6.0 release and
must not be added to the v0.5.1 surface.
