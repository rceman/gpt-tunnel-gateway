# GPT Tunnel Gateway agent handoff

This file is the post-release operational handoff for the gateway hardening
roadmap. It is intentionally self-contained; local state and secrets remain
outside Git.

## Repositories and releases

- Gateway repository: `git@github.com:rceman/gpt-tunnel-gateway.git`
- Local gateway path: `/home/therceman/git/gpt-tunnel-gateway`
- Release branch: `main`
- v0.6.0 release commit/tag target: `8996e78ebb90c011e2aa5ede8c7c32bf505a4574`
- v0.6.0 annotated tag: `v0.6.0`
- v0.6.0 tag object: `47a749da0d5cf1fe65799fbdd2f3c34bfc6f4736`
- This handoff is a post-release documentation commit on top of that
  immutable release tag. Use `git rev-parse HEAD` for the current handoff tip.
- Planner repository: `git@github.com:rceman/gpt-review-planner.git`
- Planner release: `v2.1.0`
- Planner tagged workflow commit: `900d284a97dd745d079134b49e5654b909e88c0a`
- Planner tag object: `3e43f88718bfab96c5b7713a695ad3a31a2ce780`
- The gateway workflow lock pins the planner URL, version, commit, and tag
  object above. Do not modify the planner repository as part of gateway work.

## Release lifecycle state

Gateway v0.6.1 remains in Stage A `implementation_unreleased`. The canonical
release tools are byte-identical to planner commit
`feeabecf5eb1854e9cd3ce7bb85fe6a601dc4645`:

- `scripts/release.py`;
- `scripts/check-github-ci.py`;
- `scripts/validate-release-tool-conformance.py`.

Use `check-source`, conformance, and the exact-SHA CI helper for implementation
validation. A separate owner-authorized `release_publication` task must run
prepare, release readiness, the release-only commit, exact-SHA CI, annotated
tagging, and tag verification. Do not manually edit VERSION, synchronized
version files, or dated changelog headings; Stage A never publishes.

## Runtime at handoff

- Installed gateway/controller/CLI: `0.6.0`
- Running gateway: `0.6.0`, PID `1605876`
- Preserved tunnel-client PID: `4183857`
- Gateway readiness: passing
- Tunnel readiness: passing
- `gpt-tunnelctl doctor`: `doctor: ok`
- `version_match`: true
- Gateway-only upgrade transaction:
  `upgrade-20260802T070938Z-1785654578048470697`
- Upgrade source SHA: `8996e78ebb90c011e2aa5ede8c7c32bf505a4574`
- Upgrade transition: 0.5.2 → 0.6.0; gateway `1522568` → `1605876`;
  tunnel `4183857` → `4183857`
- The active tunnel was never stopped, restarted, signalled, or replaced.

The authoritative hub is `git@github.com:rceman/typer.git` on branch
`gpt-tunnel/home_pc`. The authoritative SHA after the workflow smoke/report
commit was `7383c74edaf7e7169211bb564bc0c067c778c81f`; the final plan-closure
SHA is `fb756bb49201071632905ab9d21c6142eb20094a`. The managed checkout is an
incidental local checkout on `main` at
`c47dd1bcf11a11b65468008fcb024d468db1a62f`; these two refs are intentionally
reported separately. The managed checkout was clean. The configured projects
are:

- `gpt-github-gateway`
- `gpt-review-planner`
- `gpt-tunnel-gateway`

The deprecated `ai-workspace` project is not configured in this runtime.

## MCP and direct agent control

Live `tools/list` exposes 46 tools. The exact names are:

```text
adr_create, adr_list, adr_read, agent_send, agent_status, agent_tail,
gateway_capabilities, git_compare, git_diff, git_log, git_merge_base,
git_read_file, git_refresh, git_refs, git_show, git_tree,
git_worktree_diff, git_worktree_status, plan_cutover, plan_history,
plan_read, plan_render, plan_section_create, plan_section_delete,
plan_section_read, plan_section_update, plan_update, project_list,
project_read, project_register, project_status, run_agent_tail, run_cancel,
run_list, run_read, run_report, run_review_snapshot, run_status, run_sweep,
system_ping, task_cancel, task_create, task_dispatch, task_list, task_read,
task_supersede
```

The direct project-session contract is:

- `agent_send(project_id, message)` sends one bounded message to the session
  derived from registered project metadata; sends are serialized and return
  exact bounded output and exit information.
- `agent_tail(project_id, lines=4, skip=0)` reads one bounded window without
  retry; it does not accept a session key.
- `agent_status(project_id)` returns the normalized bounded liveness enum plus
  capacity and rate-limit warnings. The aggregated `project_status` call is
  the normal progress-check path.

CLI equivalents are:

```text
gpt-tunnel agent send <project-id> --text '<message>'
gpt-tunnel agent tail <project-id> --lines 4 --skip 0
gpt-tunnel agent status <project-id>
```

Direct controls create no durable task/run/plan/report/completion state and do
not mutate Git. Generic shell execution and caller-supplied session keys are
not capabilities. Use the durable workflow for authorized implementation.

## Upgrade and state architecture

`gpt-tunnelctl upgrade inspect` is the read-only complete target-runtime
preflight. `gpt-tunnelctl upgrade` persists a durable transaction through
inspect, prepare, backup, migrate, validate, activate, verify, and complete.
It independently records installed and running versions, preserves the tunnel
PID, restarts only the gateway, verifies readiness/doctor/MCP, and rolls back
the gateway binaries on post-install failure. After two failed activations,
enter diagnosis-only mode.

`gpt-tunnelctl state check` validates the configured-project ↔ durable-project
↔ current-plan graph and task/run invariants. Review
`gpt-tunnelctl state repair --dry-run` before any authorized apply. Immutable
workflow-v1 task/run history remains read-only; no compatibility reader,
legacy fallback, dual representation, or fabricated completion is allowed.

Useful validation commands:

```text
gpt-tunnelctl status
gpt-tunnelctl doctor
gpt-tunnelctl upgrade inspect
gpt-tunnelctl state check
gpt-tunnelctl diagnose-startup
python3 scripts/smoke_mcp.py --url http://127.0.0.1:8765/mcp
```

## Incident and history

The v0.5.0 cutover incident is documented in
`docs/incidents/2026-08-01-v0.5.0-cutover-failure.md`. The central decisions
are in `docs/adr/ADR-transactional-runtime-upgrades.md` and
`docs/adr/ADR-direct-agent-session-control.md`. Runbooks are
`docs/UPGRADE_RUNBOOK.md`, `docs/STARTUP_RECOVERY_RUNBOOK.md`, and
`docs/STATE_REPAIR_RUNBOOK.md`.

The immutable history-only runs preserved from workflow-v1 are:

- planner run `6587b760-9fc3-490d-b746-be89ceebc9b2`, task
  `3d6d473b-5093-4a15-a33b-a81ea793b8fd`;
- gateway run `5c9d118c-b50f-4548-839d-6b366f4edcfe`, task
  `1d1b259e-7d27-4d91-985e-2a86a2687404`.

Their immutable task/run files were not rewritten, and no completion or report
was fabricated for them. Their mutable dispatched task states were canonically
closed as `cancelled` by v0.5.2 state repair.

The completed workflow-2.0 smoke was:

- task: `4c3de5c4-0999-43d3-a594-9745416a4b13`;
- run: `8e5a641c-6111-4cca-b047-97ad0a6827c3`;
- task hash: `d0db9e177e5d32722eaa34bc2811b8bb77e99b32d5c8c0636483b5abc41279d1`;
- final status: `succeeded`;
- canonical report commit: `7383c74edaf7e7169211bb564bc0c067c778c81f`;
- one gateway-owned completion path; no duplicate result/evidence authority;
- direct-call task/run counts stayed 35/31 before and after the direct calls;
- the ephemeral smoke branch was pushed before finalization and deleted after
  the canonical report was published.

## Durable plans and next action

The three configured projects have valid workflow-v2 current plans with no
active task or run. The gateway plan is complete at revision `129` in the
canonical hub transaction at `fb756bb49201071632905ab9d21c6142eb20094a`.
Its queue marks incident closure, v0.5.1 hardening, v0.6.0 direct session
control, the workflow-2.0 smoke, and documentation/handoff complete; the next
original roadmap item remains awaiting explicit authorization.

The next recommended action is to read the completed gateway plan, then create
one explicitly authorized durable task for the next original orchestration
roadmap item. Do not infer or invent implementation scope from this handoff.

## v0.6.1 liveness implementation

The unreleased branch `feature/agent-liveness-compaction-recovery-v0.6.1`
implements the next authorized P1b scope from base
`05418025235949016146c0af1338052470f4d778`: aggregated project progress,
bounded liveness classifications, strict compaction detection, durable local
operational events, one-shot `run_resume`, sweep integration, and explicit
context-loss recovery instructions in execution packets. It is implementation
work only until its required gates, branch proof, completion and finalization
are complete; it is not installed, tagged, released, or activated here.

The canonical progress check is `gpt-tunnel project status <project-id>`.
The canonical recovery operation is `gpt-tunnel run resume <run-id>`; it derives
the session and recovery message and never accepts a caller-supplied session or
bare continue instruction. Read-only status/tail calls never send prompts or
append operational events. The live v0.6.0 gateway remains installed and
running on PID `1605876`; tunnel PID `4183857` remains preserved.

Owner policy: `agent_send` is bounded emergency/control-plane communication
only. It never authorizes new task scope, implementation, merge, release, or
deployment. Messages such as “implement the next feature”, “merge and release
this branch”, “deploy this”, or “continue the roadmap” must not be sent through
it; use an explicitly authorized durable task workflow.

## Compatibility policy and risks

Compatibility scope is none by default. The only permitted migrations are
explicit owner-authorized one-time persisted-state transformations. Do not add
legacy readers, aliases, fallbacks, protocol negotiation, alternate roots, or
parallel execution paths.

Remaining risks are external hub availability, Airelay/control-plane
availability, and the normal need for owner authorization before durable work.
Never expose tunnel credentials or environment contents. Preserve tunnel PID
proof for every gateway-only upgrade and keep the old rollback artifacts until
the next verified transaction establishes a new rollback point.
