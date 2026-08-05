# CLI contract

## Project

```text
gpt-tunnel project list
gpt-tunnel project read <project-id>
gpt-tunnel project status <project-id>
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
gpt-tunnel task create --file <input.json>
gpt-tunnel task list <project-id>
gpt-tunnel task read <task-id>
gpt-tunnel task dispatch <task-id> [--expected-hub-revision <sha>]
gpt-tunnel task supersede <task-id> --file <input.json>
gpt-tunnel task cancel <task-id> [--expected-hub-revision <sha>]

gpt-tunnel run list <project-id>
gpt-tunnel run read|status|report|review-snapshot <run-id>
gpt-tunnel run agent-tail <run-id> [--lines N]
gpt-tunnel run resume <run-id>
gpt-tunnel run cancel <run-id>
gpt-tunnel run sweep
gpt-tunnel run finalize <run-id> [--completion-file <gateway-owned-run-path>]
```

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
gpt-tunnel operator history <project-id> [--after-event-id <CODE-O1>] [--kind <kind>] [--limit N]
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
gpt-tunnelctl upgrade
gpt-tunnelctl diagnose-startup
gpt-tunnelctl state check
gpt-tunnelctl state repair --dry-run
gpt-tunnelctl state repair --apply
```

`upgrade inspect`, `diagnose-startup`, and `state check` are read-only. State
repair creates a hub backup before its single optimistic transaction. Upgrade
is gateway-only and preserves the tunnel-client process.
