# Hub layout

The gateway uses one canonical greenfield namespace: `gpt-tunnel/v1`.
It is compiled into the service and is not configurable. The gateway stores its private managed clone at `state_dir/hub/repository`; the configuration supplies only the Git repository URL and writable branch.

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

If the configured writable branch is absent, the gateway creates it from the remote default branch with a normal non-force push. If it already exists, the gateway preserves it exactly. The Git history of `plan/current.json` is the plan history. Tasks and ADR documents are immutable. Only task-state and run documents change lifecycle state.

No alternate layouts, legacy readers, migration paths, dual writes, adapters, or protocol negotiation are implemented. Adding any such behavior requires explicit user authorization.
