# Hub layout

The configured `hub.protocol_root` defaults to `protocol/v4`. Version 4 is additive and does not mutate historical v1-v3 records.

```text
protocol/v4/
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

The reader is protocol-root configurable so a future compatibility patch can add exact v1-v3 adapters after those schemas are imported as fixtures. This patch deliberately avoids guessing and corrupting older layouts.
