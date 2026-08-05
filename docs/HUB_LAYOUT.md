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
    runs/<run-id>/report.json
    operator-journal/counter.json
    operator-journal/events/<PROJECT-CODE>-OPR<N>.json
```

New workflow-2.0 finalization writes only the two files shown above. Existing
protocol-v1 result/evidence records remain immutable history and are not read
as completion input or copied into new reports.

`plan/current.json` is the schema-v2 compact manifest. It contains plan identity and revision, title, summary, current objective, ordered queue, section index, and active execution references; it never contains a full section description. Each `plan/sections/<section-id>.json` record contains its own revision, title, one-line short description, full description, and update metadata. Section revisions are independent optimistic concurrency domains. The Git history of both the manifest and section records is retained.

Every configured active project must have both `project.json` and a valid
`plan/current.json`. Registration creates these two records atomically with an
idle plan (empty active task/run pointers). A project is not ready for runtime
activation while either record is missing or invalid.

The owner invokes one direct schema-v1-to-schema-v2 cutover through `plan_cutover`. It writes named section records, structured objective and queue fields, and verifies that every meaningful legacy heading and content line is represented before replacing the manifest. Ordinary reads never migrate or write Git state. After cutover, only the schema-v2 layout is written and used for operational state; the bounded historical decoder may read immutable workflow-v1 task/run/ADR/operator records without admitting them into current execution. There is no operational fallback, alias, dual write, or automatic migration. Section writes rebase over unrelated manifest changes while retaining the target section revision check and manifest order.

If the configured writable branch is absent, the gateway creates it from the remote default branch with a normal non-force push. If it already exists, the gateway preserves it exactly. Tasks and ADR documents are immutable. Only task-state and run documents change lifecycle state.

No alternate operational layouts, fallback readers, dual writes, adapters, or protocol negotiation are implemented. The bounded read-only historical projections are the sole exception, and the one direct v1-to-v2 migration described above is the only migration operation.

The operator journal is an append-only context record. Its dedicated counter
and event file are written in one optimistic hub transaction; correction is a
new event with a supersession link, never an update or delete.
