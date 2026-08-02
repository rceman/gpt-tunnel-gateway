# Startup recovery runbook

Use `gpt-tunnelctl diagnose-startup` before touching a process. It reports the
failed phase, stable error code, first fatal line, process exit state, bounded
sanitized gateway logs, listener ownership, project/plan/task/run blockers,
and installed versus running versions.

If the gateway is stopped and the tunnel is healthy, repair persisted state
with `gpt-tunnelctl state check` and review
`gpt-tunnelctl state repair --dry-run`. Apply only the exact canonical proposal
after a backup and optimistic hub revision check. Never rewrite immutable
HistoricalRunV1 records or fabricate completion evidence.

Start the gateway exactly once after preflight. If it fails, retain the bounded
diagnostic, stop only the gateway if necessary, and preserve the tunnel PID.
Do not use broad process matching or restart the tunnel as a gateway recovery
step.

After a context-loss report, inspect the durable run and repository state with
the aggregated project snapshot. A read-only snapshot must not resume the
agent. If it reports `compacted_idle`, use the single canonical `run resume`
operation; if it reports `STALLED_AFTER_COMPACTION`, stop and require explicit
review. Durable operational events survive gateway restart but cannot create a
completion or report.
