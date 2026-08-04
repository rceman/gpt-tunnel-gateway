# Planner v2.1.0 integration

The gateway consumes the central planner release through the exact lock:

```text
repository: https://github.com/rceman/gpt-review-planner
version: 2.1.0
commit: 900d284a97dd745d079134b49e5654b909e88c0a
tag: v2.1.0
tag_object: 3e43f88718bfab96c5b7713a695ad3a31a2ce780
```

The planner handoff requires the gateway to preserve one workflow-v2
`completion.json` authority, target-decoder validation before shutdown,
installed-versus-running version proof, unchanged tunnel identity, durable
rollback state, no permanent compatibility readers, no fixed MCP tool count,
and strict input/output schema parity. The corresponding gateway procedures
are `upgrade inspect`, `upgrade`, `diagnose-startup`, `state check`, and
`state repair`.

Gateway `v0.5.2` keeps this exact planner pin; no planner repository mutation
or alternate workflow reader is part of the release.

Gateway `v0.6.0` uses the same tagged planner pin while adding only the
separate direct project-session transport defined by the planner policy.

The unreleased gateway `v0.6.1` keeps the exact pin and adds liveness and
context-compaction recovery. The recovery operation remains separate from the
workflow completion authority and is bounded by the durable task/run contract.

The release-side policy checks used for this integration are the planner
runtime-upgrade policy test suite and the project integration validator. They
are run against the tagged planner tree and this gateway checkout before the
gateway release commit is merged or tagged.

## Release tooling provenance

Gateway Stage A adopts the canonical release tools from planner commit
`feeabecf5eb1854e9cd3ce7bb85fe6a601dc4645`:

- `scripts/release.py`;
- `scripts/check-github-ci.py`;
- `scripts/validate-release-tool-conformance.py`.

The gateway copies their Git-object bytes and executable modes exactly, and the
conformance script rejects project-side drift. The gateway release lifecycle
remains `implementation_unreleased` until a separate owner-authorized
`release_publication` task.
