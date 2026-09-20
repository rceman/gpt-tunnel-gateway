# ADR: Machine-scoped Admin control plane

Status: accepted for GTW-TSK647

## Decision

The Gateway exposes one narrow server-mediated Admin action:

- `admin/project/onboard`

The action is available only on Linux Gateway hosts and accepts exactly:

```json
{"repository":"owner/name","code":"ABC","harness":"codex"}
```

`repository` is a canonical GitHub `owner/name` identity. The server applies its
configured GitHub owner allowlist and derives the clone URL and local directory
beneath the configured onboarding root. Callers cannot provide a URL, local
path, onboarding root, runtime key, CLI arguments, or a Lead-provisioning flag.
`code` is immutable and must be three uppercase letters. `harness` is an
allowlisted Airelay profile and defaults to `codex`.

The result is intentionally closed and compact:

```json
{
  "project_id":"widget",
  "status":"ready",
  "worker":{"key":"widget_worker","status":"ready"}
}
```

It never returns host paths, credentials, Git internals, or runtime secrets.

## Admin authentication

Admin authentication uses a durable machine-scoped Admin Session stored in the
same local SQLite Session authority as other durable Sessions, but with a
separate `admin` role, `ADM` identity format, no project binding, and no PLAW
workflow role. The host-side CLI mints a cryptographically random Admin Session
and can revoke it. The value is not a static configuration token. The MCP
layer loads and validates the persisted record on every call, rejects ended
Sessions, and permits an Admin Session only on the `admin/*` namespace. A
project-bound Planner, Lead, Advisor, or Worker Session cannot mint, assume, or
invoke that namespace. Admin Sessions cannot invoke ordinary project actions.

Admin Session mint and revoke are auditable through the runtime log. The Admin
Session remains valid before the target project exists and is never returned by
ordinary project Session listing.

## Onboarding authority and retry behavior

The action uses the existing Git and project authorities:

1. validate the Admin Session, repository identity, allowlist, code, and
   configured harness;
2. clone beneath the server-owned onboarding root, or validate the derived
   existing directory's canonical GitHub remote and clean named-branch state;
3. for an empty remote, create `main`, write only a deterministic minimal
   `README.md`, create one commit, and push it;
4. call the canonical TSK552 `ProjectOnboard` service and managed project
   registry CAS writer;
5. refresh the live project view without restarting the Gateway;
6. ensure the canonical Worker runtime and bind it through `AgentBootstrap`;
7. verify usable Airelay readiness before reporting `ready`.

A conflicting path, remote, project code, dirty worktree, detached worktree,
unsupported repository, or ambiguous runtime binding fails closed. Existing
healthy Workers are reused. Stopped Workers are resumed with the same key.
The runtime identity is `<project-folder>_worker`, independent of harness. A
harness mismatch never silently replaces a live runtime. The Airelay adapter
uses the existing bounded detached-start primitive with the exact
`start <profile> --key <key> --detached --bypass` argument contract and treats
blocked trust, unreachable controllers, and non-idle Workers as not ready.

Project registration may succeed before Worker provisioning. In that case the
result is `partial` with Worker `not_ready`; no result claims coding readiness,
and a retry converges on the existing project, runtime slot, registry entry,
and binding without duplicate authorities.

## Boundary

This control plane is not an Admin LLM, shell, filesystem browser, patch API,
Git command proxy, arbitrary URL fetcher, or project Task execution surface.
The only host effects are the bounded repository materialization, canonical
project registration, managed Worker ensure, and canonical Worker binding
required by this decision. Host secrets remain connector- or host-managed and
are not action inputs or outputs. A local Lead is deliberately outside the v1
Admin input surface; a remote Lead Session may use the ordinary MCP workflow
once the project is ready.
