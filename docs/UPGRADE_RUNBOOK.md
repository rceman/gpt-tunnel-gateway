# Runtime upgrade runbook

1. Confirm the source checkout, release identity, configured owner-only files,
   and current gateway/tunnel PIDs. Do not print environment values.
2. Run `gpt-tunnelctl upgrade inspect`. Resolve every blocker in one read-only
   pass; do not activate while any blocker remains.
3. Resolve any explicit migration or state-repair proposal, then run
   `gpt-tunnelctl upgrade` once. The transaction performs prepare, backup,
   migration bookkeeping, target validation, gateway-only activation, MCP
   verification, and durable completion. The tunnel is never restarted.
4. Confirm `installed_version`, `running_version`, and `version_match`
   independently. Confirm gateway PID changed and tunnel PID is unchanged.
5. Confirm readiness, `doctor: ok`, MCP initialize/tools/list/calls, and
   transaction status `complete`.
6. On activation failure, use the transaction rollback result. After two
   failed activations enter diagnosis-only mode; use
   `gpt-tunnelctl diagnose-startup` and do not retry automatically.

Backups and transaction state live under the configured state directory. A
temporary release directory is never the only recovery authority.

The previous-version rehearsal is executable without a runtime:
`python3 scripts/upgrade_rehearsal.py`. It covers the sanitized v0.2.2 state
graph, migration-before-shutdown ordering, rollback proof, process identity,
and MCP contract matrix.
