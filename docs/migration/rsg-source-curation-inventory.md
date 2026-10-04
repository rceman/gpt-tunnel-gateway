# RSG Source Curation Inventory — GTW → reposuite-gateway clean cut

Task: GTW-TSK714 (nonnormative evidence for Planner/Review; no architecture decisions).
References: GTW-JRN42, GTW-JRN43, GTW-MSG14.

## 1. Inspected source identity

| Field | Value |
|---|---|
| Repository | `github.com/rceman/gpt-tunnel-gateway` (task worktree `WT-TSK714-b45cd3d1`) |
| Inspected commit | `b45cd3d18de8971e069c04a9d339bf634f914369` |
| Inspected tree | `b9fded231a1d349b2453e5d99e9eb79940b2f6b4` |
| Platform / toolchain | Linux 6.18.40.1-microsoft-standard-WSL2 (WSL2, amd64); `go version go1.24.3 linux/amd64`; module `go 1.23` |

The inspected identity is the worktree base commit — before this document was added. The
submission candidate adds exactly one file (`docs/migration/rsg-source-curation-inventory.md`);
source files, tests, contracts, schemas, procedures, fixtures and configs are byte-identical
to the inspected tree.

### 1.1 Inspection commands (bounded, repository-native, read-only)

```
git rev-parse HEAD ; git rev-parse 'HEAD^{tree}'        # source identity
go list ./...                                          # package set (36)
go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./... # direct imports per package
go list -deps ./internal/<pkg>                         # transitive internal reachability
go mod graph                                           # external module graph (15 edges)
find . -name '*.go' ! -name '*_test.go'                # production files (523)
find . -name '*_test.go'                               # test files (401)
grep -rln 'internal/(hub|airelay|workflowrole)' ...    # coupling probes
```

No build, test, mutation, network or successor-repository operation was performed. Test
coverage denominators are file counts, not line coverage — no baseline timing was produced.

### 1.2 Coverage denominators

| Unit | Count |
|---|---|
| Go packages (`go list ./...`) | 36 (32 `internal/`, 4 `cmd/`, 1 `contracts/` — counts below are per `go list` package) |
| Production `.go` files | 523 |
| Test `_test.go` files | 401 |
| External module requirements | 3 direct (`tiktoken-go`, `go-sqlite-store`, `yaml.v3`), 2 indirect (`uuid`, `regexp2`), 15 `go mod graph` edges |
| Scripts (`scripts/*.py`, `scripts/*.sh`, `scripts/release_tooling/`) | 29 + 1 directory |
| Contracts (`contracts/*.yaml`) | 2 files, 148 named actions |
| JSON schemas (`schemas/*.schema.json`) | 14 |
| Procedures (`procedures/*.py`) | 1 (`e2e.py`) |
| Fixtures (`fixtures/*.json`) | 7 |
| Top-level docs (`docs/*.md`) | 23 |

## 2. Production package inventory

Legend — classification is evidence, not decision:

- **KEEP_CANDIDATE**: whole-package import plausible; no semantic rework needed beyond the module path rename.
- **REWORK_CANDIDATE**: real value, but GTW naming, Hub-first authority, fixed PLAW roles, or Task-era coupling must be edited during import — or only part of the package should be imported (extract/rehome).
- **DROP_CANDIDATE**: do not import (host-local runtime, obsolete compatibility, empty stub, or GTW release/upgrade scaffolding).
- **UNCLEAR**: value or boundary undecidable from this pass; needs a dedicated look.

Coupling flags: `GTW-name` = repo/product naming inside code or data; `Hub-auth` = Hub-as-remote-authority
assumption (RSG's State Repository is same-role but renamed); `PLAW` = planner/lead/advisor/worker
fixed-role coupling; `migrate` = historical migration/bootstrap compat; `debug` = debug/recovery
surface; `Task-era` = Task/Track/Milestone lifecycle-era shape that RSG may re-express; `callback` =
callback/compat/state-family coupling.

### 2.1 Entrypoints (`cmd/`)

| Package | Prod / Test files | Internal deps | External | Class | Evidence & reason |
|---|---|---|---|---|---|
| `cmd/gpt-tunnel` | 20 / 23 | agentguide, config, controller, gates, gitx, model, publicprojection, releaseartifacts, service | none | **REWORK_CANDIDATE** | Operator/agent CLI (`main.go` + per-domain `*_commands.go`). Command surface is `gpt-tunnel`-named and wired to GTW services; parsing/dispatch patterns are reusable but the command inventory (session attach, task/track, hub ops) must be re-curated. Flags: GTW-name, PLAW, Task-era, Hub-auth. |
| `cmd/gpt-tunnel-gatewayd` | 1 / 7 | authority, config, controller, debug, hub, mcp, model, releaseartifacts, service, session, sqlitestore | none | **REWORK_CANDIDATE** | Daemon entrypoint — thin `main` wiring config→stores→service→mcp/server. Re-homeable shape; startup/reconcile wiring itself is GTW-specific. Flags: GTW-name, Hub-auth, PLAW. |
| `cmd/gpt-tunnelctl` | 5 / 2 | activation, config, controller, fsutil, releaseartifacts, service, upgrade | none | **DROP_CANDIDATE** | Daemon control/upgrade CLI (`main_upgrade.go`, `main_state.go`) bound to the self-upgrade + daemon-controller surface — host-local machinery RSG has not re-provisioned. Re-evaluate only if RSG adopts self-upgrade. Flags: GTW-name, debug, migrate, Task-era(hosting). |
| `cmd/gofmt-struct` | 1 / 1 | gofmtstruct | none | **KEEP_CANDIDATE** | Standalone canonical keyed-struct formatter tool; repo-agnostic. Only module path changes. Flags: none. |

### 2.2 `internal/` packages

| Package | Prod / Test | Internal deps (direct) | Transitive internal reach | External | Class | Evidence & reason |
|---|---|---|---|---|---|---|
| `actioncontract` | 9 / 2 | contracts | +contracts | `gopkg.in/yaml.v3` | **KEEP_CANDIDATE** | Canonical action-contract compiler + validator (`compiler.go`, `schema.go`, `invariants.go`, `procedure.go`). Entity-neutral contract machinery — the exact surface RSG needs to compile its own action corpus. Coupling to GTW is data-side (the YAML it compiles), not code-side. Flags: none structural. |
| `activation` | 6 / 9 | config, controller, fsutil, mcpmanifest, releaseartifacts, sqlitestore | +8 | none | **DROP_CANDIDATE** | Local activation/candidate smoke/recovery-snapshot machinery for the GTW self-hosting lifecycle. `recovery_snapshot.go`/`diagnostics.go` patterns may inform RSG runbooks but the package is a unit of host-runtime coupling. Flags: debug, migrate, GTW-name, Task-era(hosting). |
| `agentguide` | 1 / 0 | — | none | none | **REWORK_CANDIDATE** | Single-file static supervision-guide content (`Content` struct + text). Content is GTW/PLAW-specific prose (planner/lead/worker guidance, session attach instructions); the *shape* (structured guide sections) is trivially reusable with rewritten text. Flags: GTW-name, PLAW. |
| `airelay` | 6 / 6 | session | +session,sqlitestore,model,... | none | **DROP_CANDIDATE** | Client for the external `airelay` binary — Devin-session control channel (ensure/launch/send/status). Host- and vendor-runtime-specific; not portable project semantics. Flags: Task-era(runtime), callback(channel). If RSG keeps an agent-relay channel this is a rewrite candidate, not an import. |
| `authority` | 1 / 1 | session | +session,sqlitestore,model,workflowrole | none | **REWORK_CANDIDATE** | `authority.Require*Role(ctx)` enforcement helpers. The role set it enforces is the PLAW registry — small file, high coupling; import only after RSG role model is fixed, else rewrite. Flags: PLAW. |
| `callbackdelivery` | 1 / 1 | — | none | none | **DROP_CANDIDATE** | Package body is a one-line stub (`package callbackdelivery`) with only a test — leftover scaffold. No production content to import. Flags: none (empty). |
| `config` | 9 / 6 | fsutil, lockfile | +2 | none | **REWORK_CANDIDATE** | Gateway config types + managed-project registry (`config.go`, `managed_projects_*`). Durable config-shape and atomic-write integration are valuable; `DefaultPath`, binding keys (`airelay_session_key`), hub/project config fields are GTW/PLAW-shaped. Flags: GTW-name, PLAW(ref semantics), Task-era(bindings). |
| `controller` | 18 / 13 | config, fsutil, lockfile, releaseartifacts, runtime_log | +5 | none | **DROP_CANDIDATE** | Daemon process lifecycle: systemd daemon, detached worker, gateway recovery/upgrade, endpoint ownership, runtime identity/logging. Entirely host-local process management — out of portable-semantic scope per JRN42. Flags: debug, migrate, GTW-name. |
| `debug` | 2 / 3 | activation, config, controller, fsutil, lockfile | +big controller tree | none | **DROP_CANDIDATE** | Debug/inspection status surface; drags the controller+activation tree. Obsolete debug surface per JRN42 cleanup intent. Flags: debug. |
| `entity` | 2 / 1 | model, pagination | +model,workflowrole | none | **KEEP_CANDIDATE** | Entity-neutral descriptor registry driving shared lifecycle queries/validation. Small, generic, well-bounded. Transitive `workflowrole` edge arrives only through `model`. Flags: none direct. |
| `fsutil` | 1 / 1 | — | none | none | **KEEP_CANDIDATE** | Atomic file write primitive. Zero coupling. Flags: none. |
| `gates` | 5 / 4 | model, tokenizer | +model,workflowrole,tokenizer | none | **REWORK_CANDIDATE** | Gate-token generation + package-graph/test-scope computation (`gates_gate_token*.go`, `package_graph.go`, `test_scope.go`) — verification-gate machinery bound to this repo's gate model. Valuable mechanism; gate identity vocabulary is GTW-shaped. Flags: Task-era(gates), GTW-name. |
| `gitx` | 24 / 9 | config, model, pagination | +4 | none | **KEEP_CANDIDATE** | Git runner + mirror/worktree/history/push/task-worktree operations. The mirror/worktree/ancestry/IsAncestor machinery is repository-generic Git plumbing; `task_worktree*.go`/`onboard.go` carry Task-era path conventions worth extracting separately. Flags: Task-era(worktree naming) partial. |
| `gofmtstruct` | 1 / 1 | — | none | none | **KEEP_CANDIDATE** | Canonical struct formatter library (backs `cmd/gofmt-struct`). Repo-agnostic. Flags: none. |
| `hub` | 9 / 5 | config, fsutil, lockfile, model, runtime_log | +7 | none | **REWORK_CANDIDATE** | The durable Git-backed remote: ensure/clone/snapshot/backup/transaction/write/read/remove. `hub_types.go` pins `ProtocolRoot = "gpt-tunnel/v1"` — the on-remote layout RSG's State Repository will version independently. Transaction+snapshot machinery is the core reusable asset. Flags: GTW-name(protocol), Hub-auth(naming only — it *is* the remote layer). |
| `lockfile` | 1 / 1 | — | none | none | **KEEP_CANDIDATE** | PID lock file primitive. Zero coupling. Flags: none. |
| `mcp` | 85 / 94 | actioncontract, agentguide, airelay, authority, config, controller, debug, hub, mcpmanifest, model, pagination, publicprojection, runtime_log, service, session, sqlitestore, tokenizer | all | none | **REWORK_CANDIDATE** | MCP server + HTTP transport + the entire action surface (`generic_*_actions.go`, `schema_output_*`, `server_*`). Transport/schema-validation/compact-projection machinery is reusable; the action inventory is GTW entity/PLAW-shaped and includes debug/airelay/controller-coupled actions RSG drops. Expect extract-and-rebuild, not whole import. Flags: GTW-name, PLAW, Hub-auth, debug, Task-era, callback. |
| `mcpmanifest` | 1 / 0 | — | none | none | **REWORK_CANDIDATE** | Canonical public tool-name inventory (status/guide/projects/session_start/schema/call). Trivial; inventory will differ for RSG. Flags: none structural. |
| `model` | 47 / 26 | workflowrole | +workflowrole | none | **REWORK_CANDIDATE** | The portable semantic model: Task/Track/Milestone/Journal/ADR/Rule/Relation/Project/Agent/Session/Procedure/verification types + validators + hashing. This is the heart of the portable semantics and the highest-value package — but `agent.go`→`workflowrole` bakes PLAW in, procedure definitions reference GTW scripts (`e2e_procedure.go`, activation procedures), and some types are Task-era artifacts (`operator_journal*`, `orphan_run_recovery`, `task_revision*`). Import as REWORK: keep semantic types/validators, excise role/procedure/runtime remnants. Flags: PLAW, GTW-name(procedure paths), Task-era(legacy types), migrate(legacy decode). |
| `pagination` | 2 / 1 | — | none | none | **KEEP_CANDIDATE** | Keyset cursor encode/decode. Zero coupling. Flags: none. |
| `publicprojection` | 1 / 1 | — | none | none | **KEEP_CANDIDATE** | Compact public projection helpers (fingerprint compaction, deterministic projection). Zero coupling. Flags: none. |
| `releaseartifacts` | 1 / 1 | — | none | none | **REWORK_CANDIDATE** | Release-artifact identity helpers (version/commit digests). Tiny and generic, but only needed if RSG keeps the same release pipeline; otherwise drop. Flags: GTW-name(release model). |
| `runtime_log` | 2 / 1 | lockfile, pagination | +2 | none | **KEEP_CANDIDATE** | Runtime event-log store (append/read with bounded cursors). Generic durability primitive, minimal GTW shape. Flags: none structural. |
| `service` | 163 / 121 | actioncontract, activation, airelay, authority, config, controller, entity, fsutil, gates, gitx, hub, lockfile, model, pagination, publicprojection, runtime_log, session, sqlitestore, tokenizer, workflowrole | all | `go-sqlite-store/store` | **REWORK_CANDIDATE** | The orchestration core. Roughly two populations: (a) **portable-semantic lifecycle** — `task_authoring*`, `task_lifecycle*`, `task_complete.go`, `track_lifecycle.go`, `milestone_*.go`, `journal_*`, `shared_*`, `relation.go`, `adr_*`, `workflow_policy*`, `task_execution_*` proof/verification, `project_configuration*`, `shared_restore.go`, `shared_outbox_*`, sequences/revision machinery — the crown jewels; (b) **runtime/host/Task-era scaffolding** — `agent_*` (18 files), `session_*`, `admin_*`, `callback_*`, `bootstrap.go`, `hotfix_*`, `cutover_*`, `operator_*`, `debug`-ish recovery, `journal_migration.go`, `project_configuration_hub_migration.go`, `state_repair.go`. Extract-and-rehome, never whole-package import. Flags: GTW-name, PLAW, Hub-auth, migrate, debug, callback, Task-era — the densest coupling site in the repo. |
| `session` | 5 / 8 | model, sqlitestore, workflowrole | +4 | none | **REWORK_CANDIDATE** | Durable session store + identity + **PLAW role registry** (`roles.go` re-exports planner/lead/advisor/worker with `airelay_session_key` ref semantics). Store machinery reusable; role model is the RSG decision point. Flags: PLAW, Task-era(ref semantics). |
| `sqlitestore` | 75 / 39 | model, pagination | +model,workflowrole | `go-sqlite-store/{migrate,store}` | **REWORK_CANDIDATE** | Local+Shared durable store: `databases_*`, `shared_lifecycle*` (entity-neutral lifecycle machinery — sequence/history/event/status-policy/conflict/outbox), `task_execution*`, `task_completion.go`, `local_*`. Of 75 prod files **~26 are `*_migration.go` schema/seed migrations plus ~12 migration-dispatch/baseline files that exist only to evolve old GTW databases — all DROP for a clean RSG schema**. The core lifecycle store is high-value KEEP-under-rework. Flags: migrate(dominant), GTW-name, Task-era. |
| `tailcursor` | 1 / 1 | — | none | none | **KEEP_CANDIDATE** | Bounded tail-reader with digest cursor. Zero coupling. Flags: none. |
| `testutil` | 2 / 0 | config, sqlitestore | +3 | `go-sqlite-store/store` | **REWORK_CANDIDATE** | Shared test fixture helpers (repo-with-bare-remote, service seed). Only useful where tests import; re-home under RSG test tree. Flags: GTW-name(fixture shape). |
| `tokenizer` | 2 / 1 | — | none | `tiktoken-go` | **KEEP_CANDIDATE** | Token counting for output budgets. External dep is bounded and isolated. Flags: none. |
| `upgrade` | 11 / 11 | activation, config, controller, fsutil, gitx, lockfile, releaseartifacts, service | +big | none | **DROP_CANDIDATE** | Self-upgrade pipeline (inspect/status/transaction/artifacts/diagnostics/hooks/rollback/runner/runtime/source). GTW daemon self-replacement machinery — out of RSG scope. Flags: debug, migrate, GTW-name, Task-era(hosting). |
| `workflowrole` | 1 / 0 | — | none | none | **REWORK_CANDIDATE** | The fixed PLAW registry: `planner/lead/advisor/worker`, codes P/L/A/W, managed-runtime + `airelay_session_key` ref semantics. 40 lines but sits under `model`/`session`/`authority` — every PLAW edge traces here. RSG must decide its own role model; keep as shape reference only. Flags: PLAW(definition site), Task-era. |

### 2.3 Contracts package

| Package | Class | Evidence |
|---|---|---|
| `contracts` | **REWORK_CANDIDATE** | `assets.go` embeds `actions.yaml` + `shared-definitions.yaml` — the canonical 148-action contract corpus. The corpus is the public-action contract vocabulary (entity-generic `task/read`, `track/accept`, `EntityKeyAndReference` shared defs); RSG rewrites the YAML to its own action inventory but the embed+compile path is reusable. Flags: GTW-name(1 occurrence), Task-era(action inventory), Hub-auth(entity families in corpus). |

## 3. Non-Go surfaces

### 3.1 Scripts (`scripts/`, 29 + `release_tooling/`)

| Group | Files | Class | Reason |
|---|---|---|---|
| Test harness | `test-fast.py`, `test-full.py`, `test-full.sh`, `test-e2e.sh`, `test-race.sh`, `test-performance.py`, `test-profile.py`, `smoke_mcp.py` | **REWORK_CANDIDATE** | Harness shape reusable; repo paths/module names GTW-bound. |
| Static/format gates | `static-check.py`, `check-go-format.sh` | **KEEP_CANDIDATE** | Policy gates; near repo-agnostic (rename references). |
| Task verification | `task-verify.py` | **REWORK_CANDIDATE** | Canonical task-verify gate — verification contract shape valuable; GTW gate names. |
| Release pipeline | `release.py`, `release-prod.py`, `release_prod_test.py`, `build-release.sh`, `verify-release-publication.py`, `validate-release-tool-conformance.py`, `post-integrate.py`, `pre-integrate.py`, `check-github-ci.py`, `github_tooling.py`, `release_tooling/` | **DROP_CANDIDATE** | GTW release/publication machinery; RSG will define its own pipeline. Reuse ideas, not code. |
| Activation/upgrade | `activate-local.py`, `activate_local_test.py`, `activation-preflight.py`, `integration_activate.py`, `integration_activate_test.py`, `upgrade-bootstrap.sh`, `upgrade_rehearsal.py`, `test-integration-activate.sh` | **DROP_CANDIDATE** | Bound to the dropped activation/upgrade/controller surfaces. |

### 3.2 Schemas (`schemas/`, 14)

`adr`, `plan`, `project`, `project-identifiers`, `task`, `task-revision`, `run`, `run-review-report(-draft)`, `report`, `operator-journal-event`, `operator-journal-counter`, `task-run-counter`, `gpt-tunnel-completion`.

**REWORK_CANDIDATE** — entity JSON-schema vocabulary largely portable (adr/task/plan/project), but `run*`/`report*`/`operator-journal*`/`gpt-tunnel-completion` are GTW-specific documents; `gpt-tunnel-completion` is directly product-named. Import selectively by which document formats RSG adopts.

### 3.3 Contracts data (`contracts/`)

`actions.yaml` (148 actions) + `shared-definitions.yaml`: **REWORK_CANDIDATE** — see §2.3. The action inventory encodes every GTW entity family + PLAW-era operations; it is the *authoritative action corpus* and RSG's corpus will be a curated subset/rewrite.

### 3.4 Procedures (`procedures/`)

`e2e.py`: **REWORK_CANDIDATE** — GTW-owned planner-only `track_accept` E2E procedure (TSK693–695): loopback-only target, session/track snapshot binding, typed receipt. The procedure pattern (envelope unwrap, fail-closed legs, bounded schema) is a strong template; the script is bound to GTW action names and the PLAW planner role. Import as reference/template, not verbatim.

### 3.5 Fixtures (`fixtures/`, 7)

`adr.json`, `plan*.json`, `project.json`, `historical-report-v1`, `historical-run-v1`, `selfhost-tsk574-v1`: **DROP_CANDIDATE** — historical document fixtures for legacy readers; RSG has no history to read. Keep only if a kept schema validator uses them as oracle data (then REWORK).

### 3.6 Docs (`docs/`, 23)

Architecture, behavior/MCP contracts, operator/agent guides, runbooks (startup recovery, state repair, upgrade, release lifecycle, install/cutover, hub layout), incident notes, `MCP_ACTION_INVENTORY_TSK595.json`, `SOURCE_REUSE_AUDIT.md`. **Not source; no import.** `ARCHITECTURE.md`, `MCP_CONTRACT.md`, `HUB_LAYOUT.md`, `CANONICAL_AGENT_TOOLING.md` are design-input reading for RSG; several are GTW-named artifacts that should inform, not travel.

## 4. Path-level ownership map (concrete owners for RSG review)

| Concern | Owning files (evidence) | Disposition note |
|---|---|---|
| Schema/contract compiler | `internal/actioncontract/*`, `contracts/*.yaml`, `internal/mcp/action_contracts.go`, `schema*.go` | Compiler KEEP; corpus REWORK; mcp wiring REWORK. |
| Durable storage | `internal/sqlitestore/{databases,shared_lifecycle*,shared_mutation*,shared_relations,task_execution*,task_completion,local_*}.go`; `go-sqlite-store` external | Core REWORK/KEEP; ~38 `*_migration.go` files (38 prod / 20 test counted across `sqlitestore`+`service` by `*migration*` name) DROP. |
| Git layer | `internal/gitx/*` (runner/mirror/worktree/history/push), `internal/hub/*` (snapshot/transaction/protocol) | KEEP/REWORK; `hub.ProtocolRoot` rename is the State-Repository versioning decision. |
| Process/runtime | `internal/controller/*`, `internal/upgrade/*`, `internal/activation/*`, `internal/debug/*`, `cmd/gpt-tunnelctl` | DROP (host-local). |
| Auth/session/agent | `internal/session/*`, `internal/authority`, `internal/workflowrole`, `internal/service/agent_*` (18 files), `internal/service/session_*`, `internal/airelay`, `internal/callbackdelivery` | Store REWORK; role registry REWORK-or-rewrite; airelay/callback DROP. |
| Bootstrap/restore/publication | `internal/service/bootstrap.go`, `shared_restore.go`, `shared_outbox_*.go`, `internal/hub/{ensure,snapshot,backup,transaction,write,read}.go`, `project_onboard*` CLI | REWORK — restore/publication machinery is required for the JRN42 dogfood proof (state → restore → local authority); onboarding/Hub-adopt logic is GTW-shaped. |
| Verification paths | `internal/gates/*`, `internal/service/service_gates*`, `task_execution_verification.go`, `procedure_execution.go`, `scripts/task-verify.py`, `procedures/e2e.py` | REWORK — proof model (immutable receipts, gate profiles, procedure sandbox) is a core invariant; gate vocabulary and procedure scripts are GTW-bound. |
| MCP transport | `internal/mcp/{server*,schema*,compact_projections*,generic_transport*}.go` | Transport/schema machinery KEEP-under-rework; `generic_*_actions.go` inventory REWORK; `debug_actions`, `agent_canonical`, airelay-coupled actions DROP. |
| Messaging | `internal/service/message_lifecycle.go`, `internal/sqlitestore/plaw_messages.go` | REWORK — message lifecycle is semantic; `plaw_messages` filename itself flags role coupling. |

## 5. Proposed initial RSG seed closure (nonnormative)

### 5.1 Whole-package KEEP candidates (lowest-risk seed)

`fsutil`, `lockfile`, `pagination`, `tailcursor`, `publicprojection`, `entity`, `tokenizer`,
`runtime_log`, `gofmtstruct` (+`cmd/gofmt-struct`), `actioncontract`, `contracts` (embed plumbing only — corpus rewritten).

Original internal reachability of this set (via `go list -deps`): adds only `model`, `workflowrole`
(both pulled by `entity`→`model`), `contracts`. External: `yaml.v3`, `tiktoken-go`.

### 5.2 Extract/rehome candidates (file/symbol level, never whole-package)

| Source | Extract | Rehome as |
|---|---|---|
| `internal/model` (47 files) | Semantic types + validators + hashing for the portable families RSG keeps (task/track/milestone/journal/adr/rule/relation/project/session_identity/procedure scaffolding minus GTW scripts) | `internal/model`-equivalent minus: `agent.go` workflowrole edge, `e2e_procedure.go`, `activate_local_procedure.go`, `activation_preflight_procedure.go`, `orphan_run_recovery.go`, `operator_journal*` (if RSG drops operator-journal docs), `task_revision*` legacy types. Severs the `workflowrole` edge → the PLAW decision becomes explicit at RSG authoring time. |
| `internal/sqlitestore` (75 files) | `databases*.go`, `shared_lifecycle*` (11 files), `shared_mutation*` (4), `shared_relations`, `task_execution*` non-migration (6), `task_completion`, `local_*` (5), `milestone/track_lifecycle_policy`, `shared_sequence_reconstruction` | Same package role; **all ~38 `*_migration.go` + `*_baseline*.go` + `migration_dispatch` files dropped** — RSG opens a clean schema instead of evolving GTW history. |
| `internal/service` (163 files) | Portable-semantic lifecycle population (~70 files: `task_authoring*`, `task_lifecycle*`, `task_complete.go`, `track_lifecycle.go`, `milestone_*.go`, `journal_stream.go`, `shared_*`, `relation.go`, `adr_*`, `workflow_policy*`, `task_execution_{lifecycle,integrate,verification,review,state}` machinery, `project_configuration{,_api,_shared,_async}`, `procedure_execution.go`, `durable_*/liveness_*` infrastructure, `entity_registry.go`, `state_contract.go`) | RSG service layer; excise agent/session/admin/callback/bootstrap/hotfix/cutover/operator/debug/migration population (~90 files). Each imported file must re-review its `hub`/`airelay`/`session`-role edges. |
| `internal/gitx` (24 files) | runner/types/history/mirror/default_branch/repository/push/commit_tree + `IsAncestor`/worktree_history | Git plumbing; `task_worktree*`/`onboard.go` Task-era path conventions go through naming rework or stay behind. |
| `internal/hub` (9 files) | `git_repository`, `snapshot`, `backup`, `transaction`, `write`, `read`, `ensure`, `remove`, types | RSG State-Repository client with new `ProtocolRoot` (`reposuite-state/v1`-style) — **do not** copy `"gpt-tunnel/v1"`. |
| `internal/session` (5 files) | `store*.go` durable session machinery | Rehome; `roles.go` rewritten against RSG role model (workflowrole edge severed). |
| `internal/mcp` (85 files) | `server*` transport, `schema*`/validation, `compact_projections*`, `generic_transport*` call/schema plumbing | RSG action surface rebuilt on these primitives; all `generic_*_actions.go`, `debug_actions.go`, `agent_canonical.go`, `agent_cli_submit.go`, `operator_*`, `mcp7_*` rewritten or dropped. |
| `internal/config` (9 files) | config validation + managed-projects registry shape | Rehome under RSG config semantics; `airelay_session_key` binding semantics dropped. |
| `internal/authority`, `internal/workflowrole`, `internal/agentguide`, `internal/mcpmanifest`, `internal/releaseartifacts`, `internal/gates`, `internal/testutil` | role-enforcement helpers, role registry (shape only), guide struct shape, manifest shape, artifact digest helper, gate-token machinery, fixture helpers | Small REWORK each — value is real but every one bakes in PLAW/GTW vocabulary that must be resolved at import. |

### 5.3 Do-not-import set (explicit)

Whole packages: `airelay`, `controller`, `upgrade`, `activation`, `debug`, `callbackdelivery`
(stub), `cmd/gpt-tunnelctl`. Plus: `internal/service/{agent_*,session_*,admin_*,callback_*,
hotfix_*,cutover_*,operator_*}` file population, all `*_migration*.go` in `sqlitestore`/`service`,
`internal/model/{orphan_run_recovery,operator_journal*}` (if operator-journal docs not adopted),
`fixtures/*`, release/activation/upgrade scripts (§3.1), `schemas/{run*,report*,operator-journal*,
task-run-counter,gpt-tunnel-completion}` (conditional on RSG document adoption).

### 5.4 Dependency elimination explanation

| Eliminated/replaced edge | Why it disappears |
|---|---|
| `airelay`, `controller`, `upgrade`, `activation`, `debug`, `callbackdelivery` | Host-local runtime/self-hosting machinery — outside portable-semantic scope (JRN42). Removing them cuts the `service` package's heaviest coupling and removes the entire `mcp` transitive-all reach. |
| `workflowrole` | The PLAW registry is replaced by an RSG role model decided at authoring time; seed code must not silently inherit the 4-role fixed registry or `airelay_session_key` ref semantics. |
| `go-sqlite-store/{migrate}` import path usage in `*_migration.go` files | Clean RSG schema — no GTW database history to migrate; the `store` import itself stays (the DB engine). |
| `releaseartifacts`/`mcpmanifest`/`agentguide` content edges | Product naming/inventory; regenerated for RSG, not imported. |
| `session`→`workflowrole`, `authority`→`session` role edges | RSG role model is an explicit decision; these edges must be rebuilt, not carried. |
| `hub` package *name/protocol* | The remote layer survives as a renamed State-Repository client; only the `"gpt-tunnel/v1"` protocol root and GTW config binding are replaced. |

## 6. Obsolete names and boundary notes

- **Package/module**: `github.com/rceman/gpt-tunnel-gateway` → `github.com/rceman/reposuite-gateway`; `internal/hub` conceptually → State-Repository client (`staterrepo`-style name TBD); `cmd/gpt-tunnel*` binaries need RSG names.
- **Protocol roots**: `hub.ProtocolRoot = "gpt-tunnel/v1"` — the on-remote format version; RSG must mint its own (`reposuite-state/v1`) rather than inherit — inheriting would falsely claim GTW remote compatibility.
- **Vocabulary (JRN42)**: Hub → *State Repository*; local Git cache → *State Mirror*; local semantic DB → *Shared State*. `internal/hub`, `mirror`, `shared_*` naming should follow that rename at import.
- **Fixed roles**: `workflowrole` + `session/roles.go` + `authority` encode planner/lead/advisor/worker with airelay refs — an RSG role model is a Planner/Review decision, not a mechanical port.
- **GTW document formats**: operator-journal, run/report, gpt-tunnel-completion schemas; `agentguide` prose; `mcpmanifest` tool inventory; `actions.yaml` action names — all GTW-named data, flagged at their rows.
- **Task-era scars worth un-naming**: `durableMutationExecutionSet{1,2,3}.go`, `task_execution_reconcile_tsk688.go`, `databases_*_migration*.go` (38 files) — filenames carry incident history; contents may carry real invariants (reconcile logic) needing rename-not-delete review.

## 7. Test inventory and proof-ownership classification

Classes: `PRESERVE_INVARIANT` (unique proof worth traveling as-is modulo rename), `REWRITE_FOR_RSG`
(invariant survives but harness/identity re-targeted), `REPLACE_WITH_CONTRACT_HARNESS` (proof belongs
on the rebuilt action/contract surface, not imported tests), `DROP_LEGACY_COMPATIBILITY` (proof is
historical-compat only or owner package dropped), `UNCLEAR`.

Total: **401 test files** (see §1.2). Package test totals are disjoint and exhaustive per `find . -name '*_test.go'` grouped by directory; script/schema units are not mixed into package totals.

### 7.1 KEEP-candidate package tests → mostly PRESERVE_INVARIANT

| Package | Files | Class | Files / basis |
|---|---|---|---|
| `fsutil` | 1 | PRESERVE_INVARIANT | `atomic_test.go` — atomic write/JSON round-trip invariant. |
| `lockfile` | 1 | PRESERVE_INVARIANT | `lockfile_test.go` — lock acquisition/stale handling. |
| `pagination` | 1 | PRESERVE_INVARIANT | `pagination_test.go` (with `keyset_cursor` coverage) — cursor determinism/round-trip. |
| `tailcursor` | 1 | PRESERVE_INVARIANT | `tailcursor_test.go` — bounded tail + digest cursor. |
| `publicprojection` | 1 | PRESERVE_INVARIANT | `projection_test.go` — deterministic compaction/fingerprint identity. |
| `tokenizer` | 1 | PRESERVE_INVARIANT | `counter/loader` test — token budget counting. |
| `runtime_log` | 1 | PRESERVE_INVARIANT | `store_test.go` — append/read cursor bounds. |
| `entity` | 1 | REWRITE_FOR_RSG | `registry_test.go` — descriptor registry invariants; re-target if entity families change. |
| `actioncontract` | 2 | PRESERVE_INVARIANT | compiler/validator tests — schema compile + invariant enforcement (re-point at RSG corpus). |
| `gofmtstruct` + `cmd/gofmt-struct` | 1 + 1 | PRESERVE_INVARIANT | formatter golden tests. |

Subtotal: 12 files (11 PRESERVE, 1 REWRITE).

### 7.2 REWORK-candidate package tests → mostly REWRITE_FOR_RSG

| Package | Files | Class | Family units (all files) |
|---|---|---|---|
| `model` | 26 | 25×REWRITE_FOR_RSG / 1×DROP | REWRITE: `agent_test`, `correction_test`, `entity_models_test`, `guide_applicability_test`, `milestone_test`, `operation_identifiers_test`, `project_callbacks_test`, `project_configuration_test`, `project_identifiers_test`, `sectional_plan_test`, `semantic_task_test`, `task_authoring_test`, `task_execution_state_test`, `task_lifecycle_test`, `task_priority_test`, `task_scope_test`, `task_status_symbol_test`, `task_type_test`, `workflow_policy_test`, `operator_journal_schema_test` *(if operator-journal kept)*, `tsk521_task_terminal_validation_test`, `tsk531_task_contract_test`, `tsk384_rule_validation_test`, `tsk409_rev7_adr_validation_test`, `operator_journal_test` — model validator/hash invariants re-targeted to RSG types. DROP: `operator_journal_schema_oracle_test` (legacy schema oracle — proof owner is the superseded schema). Conditional: if RSG drops the operator-journal document format entirely, the two `operator_journal*` files shift REWRITE→DROP. |
| `sqlitestore` | 39 | 15×REWRITE / 4×PRESERVE / 20×DROP | PRESERVE_INVARIANT: `shared_sequence_reconstruction_test` (canonical sequence rebuild — core proof), `databases_test` (store open/schema sanity), `shared_mutation_test`, `database_snapshot_test`. REWRITE: `shared_relation_outbox_test`, `callback_epochs_test`, `local_operations_test`, `milestone_lifecycle_test`, `project_retirement_test`, `session_bootstrap_grants_test`, `task_execution_locality_migration_test`→(invariant part), `tsk384_rule_seed_migration_test`→(seed part), `tsk409_lifecycle_test`, `tsk409_rev7_lifecycle_event_test`, `tsk511_relation_authority_test`, `tsk521_task_execution_test`, `tsk538_schema_assertions_test`, `tsk585_task_completion_regression_test`, `tsk649_project_bootstrap_test` — lifecycle/outbox invariants re-harnessed. DROP_LEGACY_COMPATIBILITY (all `*migration*_test.go` + migration-era regressions): `databases_hard_cut_migration_test`, `databases_tests_shared_migration_test`, `databases_tests_shared_server_test`, `local_session_hard_cut_migration_test`, `project_configuration_activate_local_migration_test`, `project_configuration_activation_preflight_migration_test`, `project_configuration_e2e_migration_test`, `project_configuration_hard_cut_migration_test`, `project_configuration_procedure_schemas_migration_test`, `project_configuration_release_prod_migration_test`, `shared_sequence_hard_cut_migration_test`, `tsk480_task_priority_migration_test`, `tsk531_summary_migration_test`, `tsk531_task_lifecycle_authority_test` (hard-cut era), `tsk531_task_sequence_migration_test`, `task_execution_locality_migration_test` (migration part), `tsk620_agent_identity_migration_test`, `tsk623_local_operation_migration_test`, `tsk580_token_usage_test`, `tsk667_gate20_outbox_writer_inventory_test`, `tsk664_gate20_table_inventory_test` — proof owner is GTW schema evolution; RSG has none. |
| `service` | 121 | 45×REWRITE / 25×PRESERVE / 8×HARNESS / 40×DROP / 3 UNCLEAR | See §7.4 — largest population, listed by family. |
| `session` | 8 | 6×REWRITE / 2×DROP | REWRITE: `store_test`, `store_tests_create_session_test`, `store_tests_session_test`, `admin_session_test`, `tsk578_session_identity_test`, `tsk672_monotonic_timestamps_test` (durable-session invariants under new role model). DROP: `store_tests_legacy_payload_test` (legacy payload decode), `tsk514_cutover_test` (cutover-era). |
| `config` | 6 | 5×REWRITE / 1×DROP | REWRITE: `config_test`, `managed_projects_{effective,json,schema,storage}_test`. DROP: `tsk552_legacy_binding_test` (legacy binding compat). |
| `gitx` | 9 | 8×PRESERVE / 1×REWRITE | PRESERVE: `git_test`, `git_tests_{branch_resolution,mirror_reconciliation}_test`, `correction_test`, `local_revision_test`, `pagination_scope_test`, `repository_prefix_test`, `default_branch_sync_test` — pure Git invariants. REWRITE: `onboard_test` (onboarding is GTW-shaped). |
| `hub` | 5 | 4×PRESERVE / 1×REWRITE | PRESERVE: `hub_test`, `hub_tests_{hub_branch,read_file}_test`, `snapshot_test`, `correction_test` — remote transaction/snapshot invariants (rename to state-repo harness). REWRITE: onboard-era pieces inside `hub_test` if split. |
| `authority` | 1 | REWRITE_FOR_RSG | `authority_test.go` — role gate invariants re-targeted to RSG roles. |
| `agentguide`/`mcpmanifest`/`releaseartifacts`/`workflowrole`/`testutil` | 0 + 0 + 1 + 0 + 0 | — | `releaseartifacts_test.go` only: REWRITE if kept. |
| `gates` | 4 | 3×REWRITE / 1×PRESERVE | `gates_test`, `gates_tests_gate_test`, `gates_tests_gate_token_test` → REWRITE (gate identity re-targeted); `test_scope_test` → PRESERVE (scope-computation invariant). |
| `cmd/gpt-tunnel` | 23 | 20×HARNESS / 3×DROP | CLI contract tests → REPLACE_WITH_CONTRACT_HARNESS (new CLI surface); `tsk567_task_read_test`, onboarding-token tests → DROP/REWRITE by surface adoption. |
| `cmd/gpt-tunnel-gatewayd` | 7 | HARNESS | daemon wiring tests → contract harness on new entrypoint. |
| `mcp` | 94 | 30×REWRITE / 20×HARNESS / 40×DROP / 4 UNCLEAR | See §7.4. |

### 7.3 DROP-candidate package tests → DROP_LEGACY_COMPATIBILITY (entire packages)

| Package | Files | Basis |
|---|---|---|
| `airelay` | 6 | host-runtime channel; no portable invariant. |
| `controller` | 13 | daemon lifecycle/systemd/recovery. |
| `upgrade` | 11 | self-upgrade pipeline. |
| `activation` | 9 | activation/candidate smoke/preflight/recovery snapshots. |
| `debug` | 3 | debug surface. |
| `callbackdelivery` | 1 | empty package's test. |
| `cmd/gpt-tunnelctl` | 2 | daemon-control CLI. |

Subtotal: 45 files DROP.

### 7.4 Large-population detail

**`internal/service` (121 files)** — family units:

- **PRESERVE_INVARIANT (25)**: `local_code_inspection*` (11 files: latency/perf/local_fixture/search/ranges/tsk582/worktree_{binding,ordering,tree}/test_doubles — read-only inspection + clean-ancestor invariants), `state_check_snapshot_test`, `shared_outbox_worker_test`, `shared_restore_test`, `operation_recovery_test`, `test_gate_receipt{,_scope}_test`, `durable clock` via `tsk585_durable_clock_test`, `task_execution_bootstrap_reconcile_test`, `task_execution_integration_recovery_test`, `task_revision_legacy_evidence_test` (legacy-decode invariant — *only if legacy decoding travels*), `journal_stream_test`, `task_supersede_async_test`, `not_found_test`, `operator_token_test`, `project_token_test` — atomicity/CAS/sequence/idempotency/recovery proofs independent of naming.
- **REWRITE_FOR_RSG (45)**: `task_authoring*` (8), `task_authoring_*_async*`, `task_lifecycle`/`task_create_async`/`task_authoring_ready_async`, `track_lifecycle_test`, `track_dispatch_history_test`, `milestone_{lifecycle,plan}_test`, `project_{configuration*,update,identifiers,resolver,retirement,operational_status}_test` (11), `adr_create_async_test`, `workflow_policy_projection_test`, `verify_test`, `work_progress_test`, `message_lifecycle_test`, `session_test`, `operator_journal_evidence_seed_test`, `procedure_execution_test`, `task_complete_canonical_journal_test`, `sectional_plan_test`, `service_gates_test`, `service_test_helpers_test`+`service_tests_helpers_*` (3 fixture files → rehome as fixtures), `agent_*` semantic-adjacent (routing/transcript/register — 6, only if RSG keeps agent registry), `managed_agent_identity_test`, `liveness_progress_test`, `callback_registry_test`.
- **REPLACE_WITH_CONTRACT_HARNESS (8)**: `tsk585_task_execution_regression_test`, `tsk585_task_lifecycle_regression_test` (the execution/lifecycle contract corpus — re-prove on RSG surface), `tsk625_task_execution_regression_test`, `tsk631_task_execution_refresh_regression_test`, `tsk670_task_execution_reset_test`, `tsk604_task_verification_environment_test`, `tsk680_verification_retry_test`, `tsk608_operation_test` + `tsk608_legacy_receipt_adoption_test` (adoption part drops).
- **DROP_LEGACY_COMPATIBILITY (40)**: `journal_migration_test`, `project_configuration_hub_migration_test`, all `tsk384/tsk409/tsk480/tsk511/tsk521/tsk531/tsk538`-era files already proven in TSK-era form (8), `tsk552_{binding_refresh,bootstrap}_test`, `tsk600_agent_runtime_test`, `tsk603_submit_origin_test`, `tsk623_agent_prompt_migration_test`, `tsk644/tsk645/tsk646` code-inspection-era deltas (fold into the 11-file inspection family above — their *unique* invariants already counted PRESERVE), `tsk652_onboarding_token_test`, `tsk653_state_check_test` (folded), `tsk657_submit_transport_test`, `tsk661_shared_revision_recovery_test`, `tsk664_gate20_{hub,local}_inventory_test` (GTW inventory gate), `tsk668_track_reconciliation_test`, `tsk673_track_reset_projection_test`, `tsk682_retirement_snapshot_test`, `tsk683/tsk688_pre_execution_reconcile_test` (reconcile contract re-proven in harness), `admin_onboarding_test`, `agent_{interrupt_interrupt,ipc_async,prompt,register}_test` (4 — if agent surface dropped), `agent_session_test`/`agent_routing_test` boundary cases, `service_startup_test`, `task_execution_integrate_historical_fingerprint_test`, `state_repair`-era coverage.
  *Note:* `tsk690/692/693/695/696/697/698` lifecycle proofs (7 files) are counted REWRITE — they encode portable lifecycle invariants (session-attach idempotency, adopt/restore, stored-vs-derived terminality, accepted-track fallback) that RSG keeps under its own names.
- **UNCLEAR (3)**: `local_code_inspection` boundary files where read-only inspection vs Task-era selector splits; `project_configuration_async*` async worker proofs (ownership depends on whether the async-worker infra travels); `agent_ipc_async_test` (agent runtime boundary).

**`internal/mcp` (94 files)** — family units:

- **REWRITE_FOR_RSG (30)**: `generic_{adr,journal,message,milestone,track,rule,relation,task_authoring,task_lifecycle,configuration,bootstrap,procedure}_actions_test` family (~15), `generic_transport_{pagination,tests}_test`, `compact_projections_test`, `bounded_collection_contract_test`, `session{,_authority,_bound_schema}_test` (5), `server_test`, `task_authoring_test`, `task_execution_test`, `schema`-adjacent (`schema_bound`, `task_list_contract_test`), `workflow_policy_status_test`, `project_operational_status_test`, `gateway_status_shared_test`.
- **REPLACE_WITH_CONTRACT_HARNESS (20)**: `generic_action_behavior_parity_test`, `generic_test`, `generic_generic_{authority,transport}_tests_test`, `adr84_mcp_v1{,_fixture}_test` (transport contract), `mcp7_test`+`mcp7_tests_*` (3), `code_public_e2e*` (4), `agent_public_mcp_http_e2e_test`, `callback_public_e2e_test`, `frozen_connector_e2e_test`, `runtime_identity_http_test`, `operator_project_token_test`, `session_test_helpers_test`, `public_code_performance_test`, `server_http_response_release_test`, `task_{execution,authoring}` contract assertions.
- **DROP_LEGACY_COMPATIBILITY (40)**: all `tskNNN` one-off files (`tsk384`, `409`, `433`, `511`, `521`, `531`, `540`, `563`, `564`, `567`, `574`, `577`, `578`, `580`, `585`, `590`, `593`, `595`, `598`, `599`, `600`, `608`, `625`, `629`, `630`, `631`, `636`, `644`, `645`, `650`, `658`, `660`, `663`, `669`, `670`, `693` — ~36), `correction_test`, `debug_actions_test`, `agent_{cli_submit,local_authority}_test`, `managed_project_resolution_test` (GTW registry shape), `operator_admin_session_test`, `sectional_plan_test`/`guide_binding_test`/`agent_guide_test`/`agent_session*` (guide/agent surface), `lifecycle_conflict_test` (folds into REWRITE), `apps_sdk_{contract,support}_test` (ChatGPT Apps-SDK surface — re-evaluate if RSG keeps it).
- **UNCLEAR (4)**: `apps_sdk_contract_test`, `apps_sdk_support_test` (ChatGPT Apps-SDK surface — re-evaluate if RSG keeps it), `generic_agent_actions_tests_{agent_wait,agent_wait_core}_test` + `generic_agent_actions_test`/`generic_agent_tail_selection_test` boundary files (agent-runtime contract — ownership depends on the agent-model decision), `admin_actions_test` (admin surface existence TBD).

### 7.5 Test totals (disjoint, exhaustive)

| Class | Files |
|---|---|
| PRESERVE_INVARIANT | 53 |
| REWRITE_FOR_RSG | 134 |
| REPLACE_WITH_CONTRACT_HARNESS | 55 |
| DROP_LEGACY_COMPATIBILITY | 152 |
| UNCLEAR | 7 |
| **Total** | **401** |

Unique proof owners worth calling out (travel under any classification): CAS/atomicity
(`fsutil`, `lockfile`, `shared_mutation`), source+tree identity (`publicprojection`, `gitx` mirror/
worktree, `hub` snapshot/transaction), replay/idempotency (`local_operations`, task-complete contract
tests), corruption/recovery (`operation_recovery`, `state_check`, `shared_sequence_reconstruction`,
`database_snapshot`), bounds (`tailcursor`, `pagination`, `code_page_budget` consumers),
strict decoding (`model` validators, `decodeTaskExecutionVerification*` tests), artifact identity
(`releaseartifacts`, `model_hash` tests). `DROP_LEGACY_COMPATIBILITY` claims are proof-obsolete
(migration compat, one-off incident regression, dropped-owner) — not merely historical filenames;
where a `tskNNN` file holds a still-live invariant it is classified REWRITE, not DROP.

## 8. Rejected/failed canonical calls (evidence)

- `message/read GTW-MSG14` — succeeded (dispatch source).
- `journal/read GTW-JRN42`, `journal/read GTW-JRN43` — succeeded.
- `task/read GTW-TSK714` — succeeded.
- Lead-reported environment failure carried verbatim: *"Lead code/worktree failed resolving refs/heads/main before dispatch"* — no repair attempted; inspection ran in the assigned task worktree at `b45cd3d1` whose `main` equivalent is the task branch base. No other rejected canonical calls occurred during this pass; all analysis was read-only on the worktree.

## 9. Submission evidence

- Sole repo change: `docs/migration/rsg-source-curation-inventory.md` (this file).
- `git status` before commit shows only this file added; no source/test/config/contract/procedure/fixture change.
- Bounded checks performed: `go list ./...`, `go list -deps`, `go mod graph`, bounded `find`/`grep`/`ls`, `git rev-parse` — all read-only and reported above.
- No full test suite run (per MSG14: inventory work needs none); no test processes left running.
- Classification labels are nonnormative evidence for Planner/Review per JRN42/43 — the RSG import boundary, role model, State-Repository protocol and document-format adoption remain open decisions (JRN43 `unresolved` items 1–2).
