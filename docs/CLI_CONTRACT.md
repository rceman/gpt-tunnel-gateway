# CLI contract

## Project

```text
gpt-tunnel project list
gpt-tunnel project read <project-id>
gpt-tunnel project status <project-id>
gpt-tunnel project workflow-policy-read <project-id>
gpt-tunnel project register --file <input.json>
```

## Plan and ADR

```text
gpt-tunnel plan read <project-id>
gpt-tunnel plan cutover --file <input.json>
gpt-tunnel plan update --file <input.json>
gpt-tunnel plan section-read <project-id> <section-id>
gpt-tunnel plan section-create --file <input.json>
gpt-tunnel plan section-update --file <input.json>
gpt-tunnel plan section-delete --file <input.json>
gpt-tunnel plan render <project-id>
gpt-tunnel plan history <project-id> [limit]
gpt-tunnel adr list <project-id>
gpt-tunnel adr read <project-id> <adr-id>
gpt-tunnel adr create --file <input.json>
```

## Task and run

```text
gpt-tunnel task create --file <input.json> # input includes operation_class
gpt-tunnel task list <project-id>
gpt-tunnel task read <task-id>
gpt-tunnel task dispatch <task-id> [--expected-hub-revision <sha>]
gpt-tunnel task supersede <task-id> --file <input.json>
gpt-tunnel task cancel <task-id> [--expected-hub-revision <sha>]
gpt-tunnel task review-report start <task-id> <run-id>
gpt-tunnel task review-report update <task-id> <run-id> <section-id> --revision <N> --file <payload.json>
gpt-tunnel task review-report validate <task-id> <run-id>
gpt-tunnel task review-report finalize <task-id> <run-id> --revision <N> [--expected-hub-revision <sha>]
gpt-tunnel task report-read <task-id> [<run-id>]

gpt-tunnel run list <project-id>
gpt-tunnel run read|status|report|review-snapshot <run-id>
gpt-tunnel run finalize <run-id> [--summary TEXT] [--deviation TEXT] [--remaining-risk TEXT]
gpt-tunnel run agent-tail <run-id> [--lines N]
gpt-tunnel run resume <run-id>
gpt-tunnel run cancel <run-id>
gpt-tunnel run sweep
gpt-tunnel run write-completion <run-id> --completion-file <receipt-input>  # bounded legacy compatibility only
gpt-tunnel run finalize <run-id> [--completion-file <gateway-owned-run-path>]
```

New operational identifiers are compact and project-coded: tasks use
`CODE-TSK<N>`, runs use `CODE-TSK<N>-RUN<M>`, ADRs use `CODE-ADR<N>`, and
operator journal events/corrections use `CODE-OPR<N>`. Each counter is a
positive safe integer owned by the adopted project. Task-create JSON requires
`slug`; the gateway derives `task/<task-id>-<slug>` and the exact remote
default-branch `base_revision`, so `branch` and `base_revision` are not caller
fields. Unpinned allocator conflicts receive bounded retries; an explicit
`expected_hub_revision` is pinned and fails fast.

`task read` first returns the active execution packet when exactly one
canonical operational run exists. For a historical task or a task without an
active run, it falls back to the task and mutable-state record. Historical
workflow-v1 task/run/ADR/operator identifiers remain read-only; they are not
accepted by execution or mutation operations.

Each active project has one revisioned workflow policy under the canonical Hub
project tree. It owns `transitional_main`/`develop_active`, the integration
branch, and CI modes for task, task-merge, and release operations. Task-create
and task-supersede inputs must declare one of `implementation`, `correction`,
`integration`, `release`, or `activation`; the gateway persists the policy
revision and effective CI projection on the immutable task. `disabled` and
`observe` are non-blocking modes: they do not authorize waiting for hosted CI.
Missing or invalid policy blocks new operational task creation rather than
falling back to a local default. `project status`, `task read`, and the active
execution packet expose the policy and its effective operation projection.
Policy adoption and updates are internal authorized service operations only;
they are not exposed as ordinary Agent CLI commands. The service requires a
trusted server-owned Planner/Delivery connection, session or capability
context outside serialized arguments; the current transport fails closed with
`AUTHORITY_UNAVAILABLE`. `updated_by` is provenance only and cannot grant
authority. Activation tasks have explicit disabled, non-blocking hosted-CI
semantics and never inherit task-merge policy. Policy reads remain available
through the normal CLI.

Delivery review is bound to the same completed Agent Run. `review-report
start`, `update`, and `validate` write only a Gateway-local atomic draft under
the configured state directory. `finalize` revalidates the immutable Task
hash, Run identity, branch, base and reviewed source head, then publishes one
`<run-id>-REPORT` report in one Hub transaction. A second report, stale draft,
stale Hub revision or changed source head is rejected without publication.
`task read` includes ordered Run summaries with Agent status, Delivery status,
outcome, reviewed head, blocker and next action. A succeeded Agent Run without
a Delivery report is explicitly `awaiting_review`; only
`accepted_reviewed_merge_ready` is merge-ready. No routine review-only Task,
Run, branch or commit exists.

`run finalize` is local-agent-only. It is intentionally not exposed as a remote MCP write tool.

## Direct project-agent session control

```text
gpt-tunnel agent send <project-id> --text '<message>'
gpt-tunnel agent tail <project-id> [--lines N] [--skip N]
gpt-tunnel agent status <project-id>
```

These v0.6.0 operations resolve the Airelay session only from registered
project configuration. A caller cannot supply a session key. `agent_tail`
defaults to four lines; `skip` omits the newest lines from the bounded window.
Messages and output are bounded, sends are serialized per session, and there
is no retry or automatic continuation. These commands create no durable task,
run, plan, report, branch, commit, or other Git mutation. `agent_send` is
emergency/control-plane communication only and never authorizes new scope,
implementation, merge, release, or deployment. “Implement the next feature”,
“merge and release this branch”, “deploy this”, and “continue the roadmap” are
misuse examples, not valid task control.

## Operator journal bootstrap

```text
gpt-tunnel operator record --file <input.json>
gpt-tunnel operator history <project-id> [--after-event-id <CODE-OPR1>] [--kind <kind>] [--limit N]
gpt-tunnel operator checkpoint --file <input.json>
```

The journal is append-only. Bootstrap `operator record` accepts only
`user_talk`, `reasoning_summary`, `task_plan`, `task_review`, and
`correction`; `operation` and `checkpoint` are reserved for later mutation
recording and the explicit checkpoint command. Records contain concise
structured context, not prompts or transcripts.

`run report` reads only the canonical workflow-2.0 report. Protocol-v1 runs
remain visible through bounded `run list`/`run read` history with legacy local
paths redacted, but report and finalization operations return a stable
history-only error for those runs.

`adr list` and `adr read` are read surfaces and preserve historical ADR
records; new ADR creation allocates only canonical `CODE-ADR<N>` identifiers.

`run resume` is the only canonical context-compaction recovery operation. It
accepts only a run ID; the gateway resolves the task, project, configured
session, branch, and recovery message. It requires one owned active run, a
confirmed compaction marker, no unanswered question, and a non-conflicted
worktree. It sends at most once for each compaction event.

`plan read` returns only the compact schema-v2 manifest. Full section descriptions are returned by `plan section-read` or the explicit `plan render` operation. Manifest updates are partial and preserve fields omitted from the input. Section updates and deletes require `expected_section_revision`; unrelated sections do not conflict.

## Git exploration

```text
gpt-tunnel git refresh <project-id>
gpt-tunnel git refs <project-id>
gpt-tunnel git log <project-id> <revision> [limit]
gpt-tunnel git show <project-id> <revision>
gpt-tunnel git tree <project-id> <revision> [path]
gpt-tunnel git read-file <project-id> <revision> <path>
gpt-tunnel git diff <project-id> <from> <to> [paths...]
gpt-tunnel git compare <project-id> <left> <right>
gpt-tunnel git merge-base <project-id> <left> <right>
gpt-tunnel git worktree-status <project-id>
gpt-tunnel git worktree-diff <project-id> [--staged]
```

## Runtime operations

```text
gpt-tunnelctl status
gpt-tunnelctl doctor
gpt-tunnelctl upgrade inspect
gpt-tunnelctl upgrade status
gpt-tunnelctl upgrade
gpt-tunnelctl diagnose-startup
gpt-tunnelctl state check
gpt-tunnelctl state repair --dry-run
gpt-tunnelctl state repair --apply
```

`upgrade inspect`, `upgrade status`, `diagnose-startup`, and `state check` are read-only.
`upgrade status` returns bounded durable transaction state or a typed no-history/corrupt-history result.
State repair creates a hub backup before its single optimistic transaction. Upgrade
is gateway-only and preserves the tunnel-client process.
