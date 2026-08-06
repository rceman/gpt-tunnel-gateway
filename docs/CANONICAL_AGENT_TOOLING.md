# Canonical agent tooling

This repository keeps proof-producing helper commands in `scripts/`. They are
the only supported surfaces for the corresponding evidence. They use bounded
typed inputs and fail closed with `BLOCKED_CANONICAL_TOOLING_GAP` when required
proof cannot be established.

| Helper | Purpose | Existing tooling was insufficient because | Canonical use | Retention |
| --- | --- | --- | --- | --- |
| `check-github-ci.py` | Prove an exact-SHA Actions run and its complete job set | The previous checker exposed only a selected job and could accept incomplete job metadata | CI gates and release verification | Retain as the canonical CI checker |
| `verify-release-publication.py` | Prove release commit, annotated tag object, peeled commit, exact-SHA CI/jobs and declared publication topology | Local tag checks did not prove remote CI/job completeness or whether a GitHub Release was unexpectedly present | Release publication gate | Retain as the canonical publication verifier |
| `load-pinned-workflow.py` | Retrieve and verify the exact workflow document named by `.gpt-workflow.lock` | Ad hoc planner-document downloads did not bind repository, commit, path and content digest in one result | Before substantial implementation/review work | Retain as the canonical pinned-workflow loader |
| `write-completion-receipt.py` | Validate and atomically place a task completion receipt at the derived task/run path | A caller-supplied completion destination permits duplicate or misplaced authorities | Agent completion preparation; final hub publication remains `gpt-tunnel run finalize` | Retain as the canonical receipt writer |

## Contracts

`check-github-ci.py --format json` returns the selected run identity and every
associated job (`id`, `name`, URL, status and conclusion). A completed run with
missing, malformed or incomplete jobs is `job_set_mismatch`; required policy
reports `BLOCKED_CANONICAL_TOOLING_GAP` and exits nonzero.

`verify-release-publication.py` reads the declared topology from
`release-config.json`. `tag_only` requires the annotated remote tag and exact
successful CI, and treats a missing GitHub Release as expected. A declared
GitHub Release must have exactly the configured assets. Authentication,
rate-limit, API, not-found and mismatch conditions remain typed; the helper
does not scrape HTML or extract IDs from rendered pages.

`load-pinned-workflow.py` has no output-format flag. Its documented invocation
is simply:

```bash
python3 scripts/load-pinned-workflow.py [--lock PATH]
```

It validates strict lock JSON, the exact HTTPS GitHub repository, lowercase
commit, safe relative document path, bounded response size and SHA-256 content
digest. The resulting provenance binds the retrieved bytes to the lock.

`write-completion-receipt.py` accepts the receipt on standard input and derives
`.gpt/run/<task-id>/run-<n>/completion.json` from the validated task and run
identities. It does not accept an output-path argument. It validates positional
gate and acceptance coverage, task hash, bounded text and duplicate/non-finite
JSON rejection, then performs an owner-only atomic write. An existing identical
receipt is idempotent; different content is rejected.

## Operating discipline

- Prefer one aggregated canonical call at an unchanged revision; do not repeat
  identical remote reads or serially reconstruct proof from weaker calls.
- Use the shared GitHub transport and typed result states. Direct `curl`, HTML
  scraping, guessed job IDs, inline publication verifiers and hand-authored
  alternate receipt destinations are prohibited.
- Keep API polling bounded. Record failed calls, rejected invocations and any
  approved tooling substitution in the run completion review; never hide a
  missing capability by adding an undocumented flag or fallback path.
- These helpers prove state and evidence only. They do not authorize release,
  merge, runtime activation, task creation or scope expansion.
