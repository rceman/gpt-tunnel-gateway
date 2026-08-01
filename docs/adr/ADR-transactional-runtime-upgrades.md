# ADR: Transactional runtime upgrades

Status: accepted

## Context

The v0.5.0 cutover installed new files without proving that persisted state,
process identity, protocol contracts, and readiness were compatible with the
target runtime. Recovery then required several activation attempts. A runtime
upgrade spans more than binary copying.

## Decision

An upgrade is one durable transaction over:

- persisted state and one-time migrations;
- release artifacts and checksums;
- installed binaries and rollback copies;
- running processes and process identity;
- configuration and protected secret files;
- protocol/tool schemas;
- readiness, doctor, and MCP verification;
- rollback state.

The canonical phases are `inspect`, `prepare`, `backup`, `migrate`, `validate`,
`activate`, `verify`, `complete`, and `rollback`. Target validation and all
required migrations finish while the old gateway is still running. A
transaction cannot be `complete` while activation or verification is pending.
State is stored beneath the configured gateway state directory; `/tmp` is
staging only.

The configured project graph must satisfy:

```text
configured active project
  ⇔ durable active project record
  ⇔ valid canonical current plan
```

Installed and running versions are independent facts. Gateway-only upgrades
must preserve the tunnel PID. Process ownership uses the controller PID file,
UID, process start time, command line, configured executable/config argument,
and an optional controller instance token; `/proc/<pid>/exe` alone is not
sufficient.

Startup diagnosis exposes a phase and first fatal error rather than only a
generic readiness timeout. Two failed activations enter diagnosis-only mode.
Permanent compatibility readers, fallback parsing, and dual canonical
execution paths are forbidden.

## Consequences

Upgrades take longer because they validate the whole target graph before
shutdown. Rollback has durable evidence and can prove that the tunnel was not
changed. Schema-changing releases require a real previous-version fixture.
External hub and control-plane failures remain explicit operational errors.
