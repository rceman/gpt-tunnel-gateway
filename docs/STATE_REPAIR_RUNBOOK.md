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
It never edits immutable task/run history, invents success, or creates a fake
completion/report.
