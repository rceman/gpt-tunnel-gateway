# Changelog

## 0.2.1 — unreleased

- Reject unsafe ADR identifiers and symlink/path escapes in hub writes.
- Separate immutable task records from mutable task-state listing.
- Enforce strict top-level MCP arguments at runtime.
- Serialize persistent sessions and publish cancellation state before Airelay delivery.
- Derive report hub commits from Git history and verify reported changed files.
- Add gateway restart rollback and fail-closed legacy hub compatibility checks.

## 0.2.0 — unreleased

- Replace the bootstrap stubs with a complete Go MCP gateway, CLI, and host-native controller.
- Add GitHub-backed project, plan, ADR, task, run, result, evidence, and report workflows.
- Add short-message Airelay dispatch to persistent coding-agent sessions.
- Add strict local-agent finalization and synthetic terminal results for failed or timed-out runs.
- Add managed read-only Git mirrors and typed cross-ref exploration tools.
- Add loopback-only Streamable HTTP MCP with object-shaped `structuredContent`.
- Add schemas, fixtures, tests, CI, migration documentation, and source-complete patch artifacts.

## 0.1.0

- Initial agent-generated bootstrap skeleton and one-time finalization helper.
