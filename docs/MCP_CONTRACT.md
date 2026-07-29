# MCP contract

The daemon implements JSON-RPC Streamable HTTP at `/mcp` and supports:

- `initialize`;
- `ping`;
- `tools/list`;
- `tools/call`.

All tool results include object-shaped `structuredContent`. Collections are wrapped in named fields such as `projects`, `tasks`, `runs`, `refs`, or `commits`.

Remote tools mirror typed CLI operations except `run_finalize`, which remains local-agent-only. No generic shell, generic Git, arbitrary path, or unrestricted file tool exists.

Git tools are read-only relative to source and remotes. `git_refresh` updates only a managed bare mirror.
