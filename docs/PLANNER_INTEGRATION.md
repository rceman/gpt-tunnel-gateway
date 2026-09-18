# Repository-local integration authority

Gateway upgrades, startup recovery, state checks, direct session transport,
and release policy are governed by the canonical GTW MCP/actions, guides,
Tasks, ADRs, Rules, and repository-local declarations. The procedures are
`upgrade inspect`, `upgrade`, `diagnose-startup`, `state check`, and `state repair`.

No external planner repository, workflow document, or remote bootstrap is
required before repository work or release validation.

## Canonical execution boundary

New work uses the canonical Task-authoring and Task-execution lifecycle:
`task/create`, `task/update`, `task/ready`, `task/dispatch`, code/tests/rebase
submissions, review, and `task/integrate`. Plan remains readable history
only. Historical Tasks, Runs, Reports, Plan, and Train/Attempt records are
preserved as evidence and are not rewritten.

## Release tooling provenance

Gateway Stage A uses the repository-local release tools:

- `scripts/release.py`;
- `scripts/check-github-ci.py`;
- `scripts/validate-release-tool-conformance.py`.

The release lifecycle remains `implementation_unreleased` until a separate
owner-authorized `release_publication` task.
