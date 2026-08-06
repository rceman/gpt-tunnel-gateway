# Gateway procedure index

| Procedure | Command | Mutation |
| --- | --- | --- |
| Inspect upgrade | `gpt-tunnelctl upgrade inspect` | none |
| Diagnose startup | `gpt-tunnelctl diagnose-startup` | none |
| Check state | `gpt-tunnelctl state check` | none |
| Preview repair | `gpt-tunnelctl state repair --dry-run` | none |
| Apply repair | `gpt-tunnelctl state repair --apply` | one backed-up hub transaction |
| Upgrade | `gpt-tunnelctl upgrade` | durable transactional, gateway-only |
| Direct agent send | `gpt-tunnel agent send` | Airelay session only |
| Direct agent tail | `gpt-tunnel agent tail` | read-only |
| Direct agent status | `gpt-tunnel agent status` | read-only |
| Aggregated project progress | `gpt-tunnel project status` | read-only |
| Canonical compaction resume | `gpt-tunnel run resume <run-id>` | one bounded Airelay recovery |
| Validate implementation release state | `python3 scripts/release.py check-source` | read-only |
| Validate release tooling provenance | `python3 scripts/validate-release-tool-conformance.py --release-script scripts/release.py --ci-script scripts/check-github-ci.py` | read-only |
| Check exact-SHA CI | `python3 scripts/check-github-ci.py --repository rceman/gpt-tunnel-gateway --sha-from-git HEAD --policy required --wait --format json` | read-only |
| Load pinned workflow | `python3 scripts/load-pinned-workflow.py` | bounded read-only |
| Verify release publication | `python3 scripts/verify-release-publication.py --repository rceman/gpt-tunnel-gateway --commit <SHA> --tag <TAG>` | read-only |
| Write agent completion receipt | `python3 scripts/write-completion-receipt.py --task-file <TASK> --run-id <RUN>` (JSON on stdin) | derived-path atomic write |

Read the relevant runbook before any mutating procedure. A gateway upgrade
never restarts tunnel-client. After two activation failures, use diagnosis-only
mode until an exact root cause is established.

The project workflow pin is planner `v2.1.0` at commit
`900d284a97dd745d079134b49e5654b909e88c0a`, recorded in
`.gpt-workflow.lock`. Runtime-upgrade, persisted-state migration, incident,
direct-session, and MCP tool-contract policy gates are consumed from that
release before source release or activation.

The helper contracts, typed failure states and prohibited ad hoc substitutions
are defined in `docs/CANONICAL_AGENT_TOOLING.md`.

Release lifecycle work has exactly two modes. Stage A v0.6.1 tooling adoption
uses `implementation_unreleased`: it validates the source and canonical tool
provenance without creating a dated changelog heading, release commit, tag, or
publication. A separate owner-authorized `release_publication` task must run
the canonical prepare, readiness, commit, exact-SHA CI, tag, and verification
sequence. Do not edit synchronized versions or dated headings manually.
