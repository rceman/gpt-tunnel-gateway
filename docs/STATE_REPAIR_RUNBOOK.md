# Durable state check and repair

`gpt-tunnelctl state check` is read-only and validates the complete graph:

- configured active projects and durable project records match;
- each active project has a strict workflow-v2 current plan;
- task/run references are two-way and dispatched tasks have exactly one run;
- active task/run pointers are operational, non-terminal, and not historical;
- immutable history is not current state.

`gpt-tunnelctl state repair --dry-run` prints the exact proposed mutable paths,
old/new hub revisions, and invariant changes without writing. Review it before
`gpt-tunnelctl state repair --apply`, which creates a backup and performs one
optimistic atomic hub transaction. Repair may clear obsolete active pointers
only when source semantics prove them stale. Configuration inventory
reconciliation is a separate owner-authorized operation and is never silently
performed by state repair.

For the v0.5.2 protocol-cutover condition, repair may also propose exactly one
mutable task-state change when all of these are true: the task state is
`dispatched`, the task has zero operational runs, at least one linked run is an
immutable `HistoricalRunV1`, and no current plan points to the task or run. The
canonical transition is `dispatched` → `cancelled`, with the reason
“close mutable dispatched state after linked run became immutable workflow-v1
history during protocol cutover”. This closes the obsolete operational state;
it does not claim success or create completion evidence.

The transaction revalidates the old state and historical-only condition under
the expected hub revision, changes only the mutable task-state path(s), and
returns the exact changed paths. It never edits immutable task/run history,
invents success, or creates a fake completion/report. If a linked operational
run exists, the repair is refused.
