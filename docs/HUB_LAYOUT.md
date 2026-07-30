# Hub layout

The gateway uses one canonical greenfield namespace: `gpt-tunnel/v1`.
It is compiled into the service and is not configurable.

```text
gpt-tunnel/v1/
  projects/<project-id>/
    project.json
    plan/current.json
    adrs/<adr-id>.json
    tasks/<task-id>.json
    tasks/<task-id>.state.json
    runs/<run-id>/run.json
    runs/<run-id>/agent-result.json
    runs/<run-id>/evidence.json
    runs/<run-id>/report.json
```

The Git history of `plan/current.json` is the plan history. Tasks and ADR documents are immutable. Only task-state and run documents change lifecycle state.

No alternate layouts, legacy readers, migration paths, dual writes, adapters, or protocol negotiation are implemented. Adding any such behavior requires explicit user authorization.
