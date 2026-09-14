# Canonical agent tooling

This repository keeps proof-producing helper commands in `scripts/` and
canonical service operations in the Go CLI. They are the only supported
surfaces for the corresponding evidence. They use bounded typed inputs and
fail closed when required proof cannot be established.

| Helper | Purpose | Existing tooling was insufficient because | Canonical use | Retention |
| --- | --- | --- | --- | --- |
| `check-github-ci.py` | Prove an exact-SHA Actions run and its complete job set | The previous checker exposed only a selected job and could accept incomplete job metadata | CI gates and release verification | Retain as the canonical CI checker |
| `verify-release-publication.py` | Prove release commit, annotated tag object, peeled commit, exact-SHA CI/jobs and declared publication topology | Local tag checks did not prove remote CI/job completeness or whether a GitHub Release was unexpectedly present | Release publication gate | Retain as the canonical publication verifier |
| `gpt-tunnel run write-completion` | Validate and atomically place a task completion receipt at the independently derived Run-specific path | Python-side task hashing and caller-selected local task/run files diverged from Gateway authority | Agent completion preparation; final hub publication remains `gpt-tunnel run finalize` | Retain as the canonical receipt writer |

## Contracts

`check-github-ci.py --format json` returns the selected run identity and every
associated job (`id`, `name`, URL, status and conclusion). A completed run with
missing, malformed or incomplete jobs is `job_set_mismatch`; required policy
reports `BLOCKED_CANONICAL_TOOLING_GAP` and exits nonzero.

`verify-release-publication.py` reads the declared topology from the exact
release commit being verified, never from a mutable worktree copy of
`release-config.json`. `tag_only` requires the annotated remote tag and exact
successful CI, and treats a missing GitHub Release as expected. A declared
GitHub Release must have exactly the configured assets. Authentication,
rate-limit, API, not-found and mismatch conditions remain typed; the helper
does not scrape HTML or extract IDs from rendered pages.

`gpt-tunnel run write-completion` accepts only a receipt input file and a
canonical Run ID. The Gateway independently reads the Task and Run from the
authoritative Hub, verifies `model.HashTask`, ownership, active operational
status, task/run identity and the canonical completion protocol, then derives
the only destination as
`<StateDir>/runs/<Run-ID>/completion.json`. The durable Run.CompletionPath is
validation input only and must equal that exact derived path. The command does
not accept a task file or an output path. It rejects sibling-run redirection,
symlink or escaping paths, duplicate/non-finite/oversized or conflicting
content, performs an owner-only exclusive atomic write with fail-closed
directory durability, and treats an identical existing receipt as idempotent.
Final hub publication remains `gpt-tunnel run finalize`.

## Operating discipline

- Prefer one aggregated canonical call at an unchanged revision; do not repeat
  identical remote reads or serially reconstruct proof from weaker calls.
- Use the shared GitHub transport and typed result states. Direct `curl`, HTML
  scraping, regular-expression extraction of Actions Run/job IDs, guessed job
  IDs, inline publication verifiers and hand-authored alternate receipt
  destinations are prohibited.
- Keep API polling bounded. Record failed calls, rejected invocations and any
  approved tooling substitution in the run completion review; never hide a
  missing capability by adding an undocumented flag or fallback path.
- These helpers prove state and evidence only. They do not authorize release,
  merge, runtime activation, task creation or scope expansion.
