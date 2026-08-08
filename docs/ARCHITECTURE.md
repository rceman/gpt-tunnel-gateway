# Architecture

## System boundary

```text
ChatGPT
  ↓ Secure MCP Tunnel
existing tunnel-client
  ↓ loopback Streamable HTTP
 gpt-tunnel-gatewayd (Go)
  ├── typed MCP API
  ├── GitHub hub transactions
  ├── managed read-only Git mirrors
  ├── local worktree inspection
  ├── project/plan/ADR/task/run service
  ├── Airelay bounded dispatch
  └── strict completion validation and canonical report derivation
```

`gpt-tunnel-gatewayd` is a control plane, not a remote shell. Every operation maps to a bounded typed method.

## Transactional runtime upgrade

`gpt-tunnelctl upgrade inspect` evaluates configuration, the configured
project/durable-project/current-plan graph, task/run invariants, process
identity, installed and live versions, listeners, hub revision, and release
artifacts in one pass. It reports every discovered blocker before activation.

`gpt-tunnelctl upgrade` persists an owner-only transaction record under
`state_dir/upgrade-transactions` and advances through inspect, prepare, backup,
migrate, validate, activate, verify, and complete. Persisted-state migrations
are explicit and one-time; a target decoder never silently accepts legacy
fields. The old gateway remains active until target state and release artifacts
are valid. Activation replaces and restarts only the gateway, and verification
requires a changed gateway PID with the exact preserved tunnel PID.

Controller-owned PID records include the process UID, start time, and instance
token. Status reports the installed binary version separately from the live
MCP version and exposes `version_match`. `/proc/<pid>/exe` is evidence only; it
is not the sole ownership proof after atomic binary replacement.

The lifecycle invariant is strict:

```text
configured active project
⇔ durable active project record
⇔ valid workflow-v2 current plan
```

Project registration writes the durable project and an idle workflow-v2 plan in
one hub transaction. `state check` validates the complete task/run graph, and
`state repair` can clear only obsolete mutable pointers after a backup.

## Durable and local state

GitHub hub state is canonical and cross-device:

- projects;
- schema-v2 compact plan manifests and independently versioned section history;
- accepted/superseding ADRs;
- immutable hashed tasks and mutable task-state records;
- run lifecycle;
- run lifecycle and one canonical report per finalized run;
- historical protocol-v1 run projections are read-only and path-redacted.

Local state is machine-specific and disposable:

- configured project roots;
- the gateway-managed hub clone under `state_dir/hub/repository`;
- managed read-only project Git mirrors;
- gateway and session mapping;
- one local `completion.json` staging path per active run;
- kernel-backed locks, PID files, and logs.

## Hub transaction

The hub config declares only `repository_url` and the writable branch. On startup, the gateway atomically creates its own clone under `state_dir/hub/repository`, verifies the exact `origin` URL, fetches refs, and creates the configured branch from the remote default branch only when that branch does not exist. Existing branches are never reset or force-updated.

Every write uses a dedicated detached temporary Git worktree:

```text
ensure managed clone and configured branch
→ fetch origin
→ resolve exact remote branch revision
→ compare expected_hub_revision
→ create detached temporary worktree
→ atomically write validated files
→ deterministic commit identity/message
→ plain non-force push
→ ls-remote verification
→ remove temporary worktree
```

The managed hub checkout is not dirtied or switched, and no user checkout of the hub repository is required.

## Git exploration

Committed history is read from managed bare mirrors so refreshes do not race with the agent's worktree. Local staged/unstaged state is exposed through separate typed worktree tools.

## Agent transport

Airelay carries only a short action and task-reading command. The complete execution packet is generated locally by `gpt-tunnel task read <task-id>`. Agents commit changes, run the required gates, push the task branch, and then write one bounded completion document; the gateway refreshes its managed mirror, uses a valid published task branch as synthetic terminal proof (or the immutable base only when that branch is absent), and writes only `run.json` and `report.json` to the hub. The persistent session is owner-managed; the gateway never starts or resumes Codex.

The v0.6.0 direct project-session surface is separate from that durable
workflow. `agent_send`, `agent_tail`, `agent_transcript`, and `agent_status` resolve a configured
project to its registered Airelay session, serialize sends with a local
kernel-backed lock, bound all message/output windows, and return the exact
bounded Airelay result. They do not create task/run/plan records or mutate Git,
and caller-supplied session keys and generic shell execution are impossible.

## Identifier allocation

Operational identifiers are canonical compact values: `CODE-TSK<N>` for tasks,
`CODE-TSK<N>-RUN<M>` for runs, `CODE-ADR<N>` for ADRs, and `CODE-OPR<N>` for
operator records and corrections. Project adoption initializes the task and
ADR allocation records; run and operator counters are maintained with their
own optimistic hub transactions. All counters are bounded by the positive
JavaScript-safe integer maximum, so the maximum value may be allocated once
but can never be reused. Unpinned conflicts retry within the shared bounded
allocator limit, while pinned writes fail immediately.

Task creation requires a validated slug. The gateway derives the task branch
from the allocated task ID and slug and resolves the task base from the
authoritative remote default branch; branch/base overrides are not an
operational input. Workflow-v1 identifiers and history records remain
read-only inputs to list/read surfaces and are excluded from execution,
session ownership, dispatch, cancellation, sweep, and other mutations.

The v0.6.1 liveness layer aggregates plan/task/run/repository and bounded
Airelay evidence in `project_status`. It classifies the session with one fixed
enum and recognizes compaction only from a completed marker plus an active
nonterminal run, reachable session, no explicit question, and no meaningful
post-marker output. Resume is a gateway-generated `run_resume` operation. Its
bounded operational event log is local durable evidence for restart recovery,
never a completion or report authority; read-only status calls never append to
it. Status, tail, repository and bounded hub components are fetched in
parallel under one request deadline, and failures are returned as sanitized
component codes alongside the available snapshot.

## Runtime binding

The controller starts `tunnel-client` with a canonical `MCP_SERVER_URL` derived from the configured loopback gateway address and a configured loopback health listen address. The owner-managed mode-0600 env file contains only control-plane credentials and explicitly allowed tunnel options.
