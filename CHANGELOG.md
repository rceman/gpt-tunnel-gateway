# Changelog

## Unreleased

- Bound startup state validation to one run-index load per project and add bounded target-startup rollback diagnostics.
- Add fail-closed upgrade argument parsing and read-only durable upgrade status reporting.

## 0.6.2 — 2026-08-06

- Add canonical project-scoped TSK/RUN/ADR/OPR identifiers with bounded allocator and cutover behavior.
- Add Run-bound Delivery review draft, finalization, read and discovery authority.
- Enforce latest-Run precedence for Delivery review reads and merge-ready admission.
- Protect immutable Delivery reports from stale complete Agent machine snapshots during publication.
- Enforce strict closed report model/schema bounds, the exact finding severity enum, and Unicode code-point parity.

## 0.6.1 — 2026-08-04

- Adopt the planner-canonical two-mode release lifecycle and exact Git-object
  provenance/conformance checks for the gateway’s Stage A
  `implementation_unreleased` tooling.
- Add v0.6.1 aggregated project progress with bounded task/run, repository,
  Airelay liveness, activity, blocker and next-action data.
- Add deterministic agent-state and context-compaction detection with durable
  bounded operational events and one-shot canonical `run_resume` recovery.
- Add context-loss recovery instructions to rendered task execution packets,
  CLI/MCP parity, schema coverage, and isolated liveness tests.

## 0.6.0 — 2026-08-02

- Add bounded project-level `agent_send`, `agent_tail`, and `agent_status`
  controls over registered Airelay sessions.
- Keep direct session control separate from durable tasks, runs, plans and Git;
  derive session keys from configured project metadata and serialize sends.
- Expose the direct-session contract through matching CLI/MCP schemas and
  bounded capacity-aware status results.

## 0.5.2 — 2026-08-02

- Repair mutable dispatched task state when its only linked runs are immutable
  workflow-v1 history, while preserving all immutable task/run records.
- Exclude HistoricalRunV1 records from current session ownership, dispatch,
  cancellation, sweep, task-read and operational state-repair decisions.
- Extend the previous-version rehearsal with the history-only task-state
  repair condition.

## 0.5.1 — 2026-08-01

- Harden runtime upgrades with complete target-state preflight, durable transaction records, gateway process identity proof, installed-versus-running version reporting, bounded startup diagnosis, state check/repair, atomic project-plan registration, and MCP schema/output parity validation.

## 0.5.0 — 2026-08-01

- Publish workflow 2.0 with one gateway-owned completion file, planner-2.0 positional receipts, gateway-derived mirror-backed proof, and one canonical report; terminal `needs_gpt_revision`, read-only protocol-v1 history, push-before-finalize, and removal of normal result/evidence authority.

## 0.4.0 — unreleased

- Replace monolithic plan storage with a schema-v2 compact manifest and independently editable sections, including direct migration and typed CLI/MCP operations.
- Add read-only active-run Airelay tail inspection through MCP and CLI.

## 0.3.0 — unreleased

- Add the read-only `run_review_snapshot` aggregate for bounded structural review of active and terminal runs.

## 0.2.3 — unreleased

- Add safe transactional `gpt-tunnelctl upgrade` with validation, rollback, and gateway-only restart.

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
