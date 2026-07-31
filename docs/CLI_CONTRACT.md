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
gpt-tunnel run cancel <run-id>
gpt-tunnel run sweep
gpt-tunnel run finalize <run-id> [--completion-file <path>]
```

`run finalize` is local-agent-only. It is intentionally not exposed as a remote MCP write tool.

`run report` reads only the canonical workflow-2.0 report. Protocol-v1 runs
remain visible through bounded `run list`/`run read` history with legacy local
paths redacted, but report and finalization operations return a stable
history-only error for those runs.

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
