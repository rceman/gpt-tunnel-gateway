# Hub layout

The gateway uses one canonical greenfield namespace: `gpt-tunnel/v1`.
It is compiled into the service and is not configurable. The gateway stores its private managed clone at `state_dir/hub/repository`; the configuration supplies only the Git repository URL and writable branch.

```text
gpt-tunnel/v1/
  projects/<project-id>/
    project.json
    plan/current.json
    plan/sections/<section-id>.json
    adrs/<adr-id>.json
    tasks/<task-id>.json
    tasks/<task-id>.state.json
    runs/<run-id>/run.json
    runs/<run-id>/agent-result.json
    runs/<run-id>/evidence.json
    runs/<run-id>/report.json
```

`plan/current.json` is the schema-v2 compact manifest. It contains plan identity and revision, title, summary, current objective, ordered queue, section index, and active execution references; it never contains a full section description. Each `plan/sections/<section-id>.json` record contains its own revision, title, one-line short description, full description, and update metadata. Section revisions are independent optimistic concurrency domains. The Git history of both the manifest and section records is retained.

The gateway performs one direct schema-v1-to-schema-v2 migration when it encounters the current legacy plan. Migration writes named section records and verifies that the complete legacy body is preserved before replacing the manifest. After that cutover, only the schema-v2 layout is read or written; there is no fallback, alias, dual write, or legacy reader.

If the configured writable branch is absent, the gateway creates it from the remote default branch with a normal non-force push. If it already exists, the gateway preserves it exactly. Tasks and ADR documents are immutable. Only task-state and run documents change lifecycle state.

No alternate layouts, fallback readers, dual writes, adapters, or protocol negotiation are implemented. The one direct v1-to-v2 migration described above is the only migration operation.
