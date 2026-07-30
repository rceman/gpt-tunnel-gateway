# Changelog

## 0.2.2 — unreleased

- Accept bounded protocol `_meta` in `tools/call` while retaining strict rejection of unknown envelope fields.
- Declare exact per-tool `outputSchema` contracts and validate successful `structuredContent` before returning it.
- Declare explicit read-only, destructive, idempotent, and open-world annotations for every MCP tool.
- Keep tool errors outside `structuredContent` so successful output schemas remain authoritative.

## 0.2.1 — unreleased

- Reject unsafe ADR identifiers and symlink/path escapes in hub writes.
- Separate immutable task records from mutable task-state listing.
- Enforce strict top-level MCP arguments at runtime.
- Serialize persistent sessions and publish cancellation state before Airelay delivery.
- Derive report hub commits from Git history and verify reported changed files.
- Add gateway restart rollback.
- Remove unauthorized compatibility infrastructure and make `gpt-tunnel/v1` the only hub namespace.
- Add crash-safe kernel locks, gateway ownership enforcement, canonical tunnel-client bindings, and relocatable release checksums.
- Replace the required user-managed hub checkout with an atomic gateway-managed clone under `state_dir`.

## 0.2.0 — unreleased

- Replace the bootstrap stubs with a complete Go MCP gateway, CLI, and host-native controller.
- Add GitHub-backed project, plan, ADR, task, run, result, evidence, and report workflows.
- Add short-message Airelay dispatch to persistent coding-agent sessions.
- Add strict local-agent finalization and synthetic terminal results for failed or timed-out runs.
- Add managed read-only Git mirrors and typed cross-ref exploration tools.
- Add loopback-only Streamable HTTP MCP with object-shaped `structuredContent`.
- Add schemas, fixtures, tests, CI, installation/cutover documentation, and source-complete patch artifacts.

## 0.1.0

- Initial agent-generated bootstrap skeleton and one-time finalization helper.
