# MCP contract evidence

The self-contained runner passed two isolated full-gate cycles, release-version
checks, and MCP contract smoke before applying the patch. The real-branch gates
also passed, including race tests, static checks, release checksums, and binary
version checks.

Base: `5bf4f2fae46d748c89b8a03d057b8d0026616933`
Implementation: `b0f31257fd494b7801c1fbf808db15ed0f75e2c9`
Integration fixes: none.

The patch provides exact object-shaped output schemas and explicit annotations
for all 36 tools, strict envelope/tool argument validation, and version 0.2.2
coherence. No active gateway restart, tunnel-client operation, app refresh, or
runtime configuration change occurred.
