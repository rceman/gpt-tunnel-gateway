# Repository-local integration authority

Gateway upgrades, startup recovery, state checks, direct session transport,
and release policy are governed by the canonical GTW MCP/actions, guides,
Tasks, ADRs, Rules, and repository-local declarations. The procedures are
`upgrade inspect`, `upgrade`, `diagnose-startup`, `state check`, and `state repair`.

No external planner repository, workflow document, or remote bootstrap is
required before repository work or release validation.

## Train v2 adoption boundary

Train v2 is an explicit per-project cutover, not an implicit consequence of
deploying the A-E implementation. The Gateway must smoke the exact active
runtime before `train/cutover`; the cutover receipt records the project
configuration revision, source/runtime heads, action-schema revision,
historical compatibility, and the required Plan materialization decision.
Until that receipt exists, legacy execution remains the writable authority.
After it exists, Plan remains readable history only and new work uses the
branchless Task/Train lifecycle. Historical Tasks, Runs, Reports and Plan
records are not rewritten.

## Release tooling provenance

Gateway Stage A uses the repository-local release tools:

- `scripts/release.py`;
- `scripts/check-github-ci.py`;
- `scripts/validate-release-tool-conformance.py`.

The release lifecycle remains `implementation_unreleased` until a separate
owner-authorized `release_publication` task.
