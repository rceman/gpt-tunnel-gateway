# Release lifecycle contract

This document is the canonical policy for version and release work. It defines
exactly two lifecycle modes:

1. `implementation_unreleased` — an owner-selected next source version may be
   present in every configured version field while accepted notes remain under
   `## Unreleased`. This mode must not create a dated target heading, release
   commit, tag, GitHub Release, or publication.
2. `release_publication` — the owner-authorized publication path promotes the
   accepted `Unreleased` notes to a dated target heading, creates the actual
   release diff, validates readiness, and performs tagging/publication only
   when separately authorized.

There is no manual, third, fallback, alias, or compatibility lifecycle mode.

## Gateway Stage A

Gateway v0.6.1 tooling adoption is an `implementation_unreleased` operation.
The adopted `scripts/release.py`, `scripts/check-github-ci.py`, and
`scripts/validate-release-tool-conformance.py` files are byte-identical to the
planner Git objects at commit
`feeabecf5eb1854e9cd3ce7bb85fe6a601dc4645`. Their executable modes are part
of the project contract. Stage A may validate source state and tooling
conformance, but it must not create a dated heading, release commit, tag, or
GitHub Release.

The later `release_publication` task is separate owner-authorized work. It
must use the exact command order below and must not be inferred from a
successful Stage A check. Manual dated headings, synchronized-version edits,
release commits, and tags are prohibited in `implementation_unreleased` mode.

## Task declaration

The immutable task and its task-specific handoff record must carry exactly one
of these declaration pairs:

```text
Release lifecycle mode: implementation_unreleased
Release target version: X.Y.Z
```

or:

```text
Release lifecycle mode: release_publication
Release target version: X.Y.Z
```

Reusable policy documents do not select a concrete version.

## State commands

The dependency-free canonical implementation is `scripts/release.py`.

```text
implementation_unreleased
→ python3 scripts/release.py check-source
→ implementation gates and review

release_publication
→ python3 scripts/release.py prepare <TARGET_VERSION>
→ python3 scripts/release.py check-release-ready
→ python3 scripts/release.py commit
→ exact release-commit CI
→ python3 scripts/release.py check-tag-ready
→ python3 scripts/release.py tag
→ python3 scripts/release.py verify-tag <TAG>
→ separately authorized publication
```

`check-source` requires synchronized semantic versions, one non-empty
`Unreleased` section, no dated heading for the current version, no target tag,
and a clean worktree. `prepare` fully validates before changing bytes and
supports both a lower current version and a pre-set target version. In the
pre-set case configured version files remain byte-identical and the release
commit may contain only the changelog.

`check-release-ready` runs after `prepare` and validates the prepared diff
before `commit`: it requires the target heading, an empty `Unreleased` section,
synchronized version files, and no unrelated changed path. It does not run
before preparation or release mutation.
`check-tag-ready` additionally requires a clean worktree, the configured
release commit subject and changed-path set, and an absent target tag.
`tag` creates only an annotated tag. `verify-tag` rejects lightweight tags,
wrong names, wrong versions, and tags resolving to another commit.

All validation completes before writes. Preparation renders every target byte
in memory and applies the set with rollback-safe atomic replacement. A failed
application restores the original bytes and leaves no partial release state.

## Task and review enforcement

Any task touching configured version files, the changelog, release tooling or
configuration, runtime/package version fields, release metadata, or tag
behavior must contain exactly these immutable declaration strings:

```text
Release lifecycle mode: implementation_unreleased
Release target version: X.Y.Z
```

or:

```text
Release lifecycle mode: release_publication
Release target version: X.Y.Z
```

The planner-managed task checker reads only the immutable task's `constraints`
and `required_gates` projection; there is no standalone lifecycle schema or
alternate gate alias. An
`implementation_unreleased` task must declare exactly one canonical
`check-source` gate, and may declare the exact consistency check and exact
SHA-from-Git CI gate, but must not declare publication mutation commands. A
`release_publication` task must declare the exact ordered conformance,
prepare, `check-release-ready`, commit, SHA-from-Git CI, `check-tag-ready`,
tag, and `verify-tag` gates. GPT review must not emit `MERGE_READY` until the
exact declaration, state-specific gate, two-script conformance proof, and
ordered publication sequence are present and consistent.

Project-local release tooling, when present, must pass:

```bash
python3 scripts/validate-release-tool-conformance.py \
  --release-script scripts/release.py \
  --ci-script scripts/check-github-ci.py
```

The project script must be byte-identical to the planner-owned canonical
implementation, and the project CI helper must be byte-identical to the
planner-owned canonical helper. Both scripts must advertise their exact
supported interfaces. No project may silently diverge with a simplified
release or CI implementation.

## Safety invariants

- all configured version files must agree before and after a lifecycle step;
- downgrade, malformed/empty/multiple `Unreleased` headings, conflicting target
  headings or dates, repeated promotion, and an existing target tag fail before
  mutation;
- release commits contain only the actual non-empty subset of configured
  release files plus the changelog and use the configured message;
- release publication is never inferred from a source-state success;
- `MERGE_READY` is not release completion and cannot bypass lifecycle proof;
- release behavior remains compatibility scope `none` unless a separate owner
  instruction authorizes a bounded migration declaration.
