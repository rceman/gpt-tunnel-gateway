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
gpt-tunnel plan update --file <input.json>
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
gpt-tunnel run read|status|report|evidence <run-id>
gpt-tunnel run cancel <run-id>
gpt-tunnel run sweep
gpt-tunnel run finalize <run-id> [--result-file <path>] [--evidence-file <path>]
```

`run finalize` is local-agent-only. It is intentionally not exposed as a remote MCP write tool.

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
