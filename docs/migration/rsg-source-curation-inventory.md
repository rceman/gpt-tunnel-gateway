# RSG Source Curation Inventory — GTW → reposuite-gateway clean cut

Task: GTW-TSK714 (rework per GTW-MSG15). **All classifications are nonnormative evidence for
Planner/Review** — the RSG import boundary, role model, remote-authority model, document formats
and runtime-surface adoption remain open decisions per GTW-JRN42/43.

## 1. Inspected source identity

| Field | Value |
|---|---|
| Repository | `github.com/rceman/gpt-tunnel-gateway`, task worktree `WT-TSK714-b45cd3d1` |
| Inspected commit | `b45cd3d18de8971e069c04a9d339bf634f914369` |
| Inspected tree | `b9fded231a1d349b2453e5d99e9eb79940b2f6b4` |
| Platform / toolchain | Linux 6.18.40.1-microsoft-standard-WSL2 (WSL2 amd64); `go1.24.3 linux/amd64`; module `go 1.23` |

The inspected identity is the worktree base commit before this document existed. The submission
candidate adds only `docs/migration/rsg-source-curation-inventory.md`; all other files are
byte-identical to the inspected tree.

### 1.1 Measurements (frozen, supplied by Lead — not recomputed)

- `go list -json` output frozen at `shell-353427-da77d010745c7041/content.txt`; parsed summary at
  `shell-df6e0a-0d413c2542912dba/content.txt` (two successive JSON values).
- **36 packages** = 31 `internal/` + 4 `cmd/` + `contracts`.
- **523 production `.go` files**.
- **401 test files** = 394 active (`TestGoFiles`) + 7 build-tag-excluded (`IgnoredGoFiles`:
  5×`//go:build livee2e` in `cmd/gpt-tunnel-gatewayd`, 2×`//go:build liveperformance` in
  `internal/mcp` and `internal/service`).
- External module requirements: **3 direct** (`pkoukk/tiktoken-go v0.1.8`,
  `rceman/go-sqlite-store v0.0.0-20260817182756`, `yaml.v3 v3.0.1`) + **2 indirect**
  (`uuid`, `regexp2`); `go mod graph` has 15 edges.
- **Dependency kinds distinguished:** *direct external Go imports* (module packages the
  package itself imports), *transitive Go modules* (reachable via `Deps` in the frozen
  `go list -json`), and *runtime dependencies* (external tools invoked by owned features —
  reachable by graph, exercised only on the feature's call path).
- **Runtime/build dependencies beyond the Go toolchain** (verified from source):
  - `libsqlite3` via **CGO** — `go-sqlite-store/internal/sqlite3c/sqlite.go` has
    `#cgo pkg-config: sqlite3` + `#include <sqlite3.h>` → requires `pkg-config`+libsqlite3 for
    `internal/sqlitestore`, `internal/service`, `internal/testutil`, `internal/session` (transitive).
  - `git` executable — `internal/gitx/git_runner.go`, `internal/controller/lifecycle.go`,
    `internal/debug/status.go` spawn `exec.Command` git.
  - `airelay` binary — `internal/airelay/client_airelay_{ensure,launch}.go` spawn it.
  - `systemd` — `internal/controller/systemd_daemon*.go`.
  - `python3` — `internal/service/procedure_execution.go` runs repo procedure scripts;
    `scripts/*.py` tooling.

### 1.2 Commands actually run (evidence record)

| Command | Result |
|---|---|
| `git rev-parse HEAD`, `git rev-parse HEAD^{tree}` | identity above |
| `go list ./...` | 36 packages (matches frozen) |
| `go list -f '{{.ImportPath}}\|{{join .Imports " "}}' ./...` | direct imports per package (matches frozen) |
| `go list -deps ./internal/<pkg>` (per package) | transitive internal reach (matches frozen) |
| `go mod graph` | 15 edges |
| `find`/`ls`/`grep`/`sed` (bounded, read-only) | file inventory, symbol/path verification, test-function extraction (`func Test\w+` per file) |
| `python3 scripts/static-check.py` | `STATIC_CHECK_OK` (bounded static gate; **was run**, correcting v1's contradictory "no checks" claim) |
| `go run ./cmd/gofmt-struct -check docs/` | **exit 2** — the tool invocation was attempted and the tool may have partially run; the flag/path combination was rejected (stderr not captured; exact cause not asserted). No source effect. |
| `cat`/`grep` on `go-sqlite-store` module cache | CGO/libsqlite3 evidence |
| Read-only SQLite/cat inspection of `internal/lockfile`, `internal/entity`, `internal/actioncontract`, `internal/publicprojection`, `internal/hub` sources | coupling evidence cited below |

No production/test build suite was executed (the one `go run` tool invocation above is recorded
honestly — it is not a build of product code). No test execution, no mutation, no network, no
successor-repository operation. Test counts are file counts; no line coverage or timing
baseline produced.

### 1.3 Denominators

| Unit | Count |
|---|---|
| Go packages | 36 |
| Production `.go` files | 523 |
| Test `_test.go` files | 401 (394 active + 7 ignored) |
| Migration-named prod files (`*migration*.go`) | **38** (36 `internal/sqlitestore` + 2 `internal/service`) |
| Migration-named test files | **21** |
| Scripts | **29 top-level** + `scripts/release_tooling/` (**10 modules**: `changelog`, `cli`, `configuration`, `foundation`, `lifecycle_checks`, `lifecycle_tags`, `repository_state`, `transaction`, `version_files`, `__init__`) = **39 files** |
| Contracts | 2 YAML files, 148 named actions (`contracts/actions.yaml` — counted `name:` entries) |
| JSON schemas | 14 |
| Procedures | 1 (`procedures/e2e.py`) |
| Fixtures | 7 JSON |
| Top-level docs | 23 |

## 2. Production package inventory

**Class semantics** (evidence labels, not decisions):
- **KEEP_CANDIDATE** — whole-package import plausibly survives with *only* a module-path rename:
  no GTW-naming, authority-model, role-model or family-set coupling found in its own source.
- **REWORK_CANDIDATE** — real reusable content, but named couplings below must be edited at
  import; or only a file/symbol subset is worth extracting (extract/rehome), which is stated.
- **DROP_CANDIDATE** — no reusable production content found (empty/stub), or the package is
  wholly bound to a surface no evidence says RSG will adopt.
- **UNCLEAR** — value depends on an undecided RSG surface; stated explicitly.

**Coupling axes** (each row declares every axis Yes/No/NA with evidence — never omitted):
`GTW-name` = product/repo naming embedded in code or constants; `Hub-auth` = remote-as-authority
or remote-family coupling (see §4); `PLAW` = fixed planner/lead/advisor/worker role coupling;
`migrate` = historical migration/bootstrap-compat code; `debug` = debug/recovery surface;
`callback` = callback/compat/state-family coupling; `Task-era` = entity/workflow-era shape RSG
may re-express; `runtime-dep` = external runtime requirement.

### 2.1 Whole-package KEEP_CANDIDATE rows (8)

| Package | Prod/Test | Direct internal | Direct external / runtime | Coupling axes (all) |
|---|---|---|---|---|
| `internal/fsutil` | 1/1 | none | none | GTW-name **No**; Hub-auth **No**; PLAW **No**; migrate **No**; debug **No**; callback **No**; Task-era **No**; runtime-dep **No**. Atomic JSON/file write primitive. |
| `internal/lockfile` | 1/1 | none | none | All axes **No**. PID lockfile; concurrent-owner/reacquire + kernel-owner evidence proven by `lockfile_test.go`. |
| `internal/pagination` | 2/1 | none | none | All axes **No** (GTW tokens only in test fixture strings — test data, not source). Keyset cursor encode/decode + scope binding. |
| `internal/tailcursor` | 1/1 | none | none | All axes **No**. Bounded tail reader + digest cursor. |
| `internal/runtime_log` | 2/1 | lockfile, pagination | none | GTW-name **No** (only module-path imports); all other axes **No**. Append/read event log + redaction — generic. |
| `internal/gofmtstruct` + `cmd/gofmt-struct` | 1+1/1+1 | gofmtstruct | none | All axes **No**. Canonical struct-formatter tool; repo-agnostic. |
| `internal/tokenizer` | 2/1 | none | `tiktoken-go` (embedded vocab loader; offline) | GTW-name **No**; all other axes **No**; runtime-dep **tiktoken encoding embedded** (no service call). Token-budget counting. |

### 2.2 REWORK_CANDIDATE rows (21 here + 5 runtime-audited in §2.3 = 26)

| Package | Prod/Test | Direct internal | Transitive internal (n) | Direct external / runtime | Required rework (evidence) | Coupling axes |
|---|---|---|---|---|---|---|
| `internal/actioncontract` | 9/2 | contracts | 1 | `yaml.v3` | `canonical.go:7` imports `contracts` (the GTW YAML corpus); `compiler.go:14` `requiredDefinitions` enumerates GTW shared-definition names (`Track`, `Milestone`, `TaskExecution`...); `compiler.go:117,129` retired-domain/action checks and `:966` `retiredFamilies` encode GTW history; `types.go:8` `formatTimestamp = "gtw-timestamp"`. Compiler engine reusable; corpus-binding must be re-pointed. | GTW-name **Yes**; Hub-auth **No** (corpus-data only); PLAW **No**; migrate **Yes** (retired checks); debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/entity` | 2/1 | model, pagination | 3 | none | `registry.go:16` `protocolRoot = "gpt-tunnel/v1/projects"`; `descriptor.go` enumerates concrete GTW entity families (task/track/milestone/journal/adr/rule/...). Registry machinery generic; protocol root + descriptor set are GTW data. | GTW-name **Yes**; Hub-auth **Yes** (protocol root names remote layout); PLAW **No**; migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/publicprojection` | 1/1 | none | 0 | none | `projection.go:160-168` hard-codes legacy action IDs (`git_show`, `git_diff`, `git_worktree_diff`, `code/diff`); `:318ff` `omitNonGitIdentifier` enumerates GTW semantic field names (`gate_identity`, `task_digest`, `gate_profile`, `*_sha256`/`_digest`). Projection machinery generic; action/field vocabulary GTW-bound. | GTW-name **Yes**; Hub-auth **No**; PLAW **No**; migrate **Yes** (legacy action IDs); debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/gitx` | 24/9 | config, model, pagination | 6 | `git` executable | `task_worktree*.go`/`onboard.go` encode Task-era worktree naming and GTW onboarding; `task_integration.go` binds the task-integration commit flow; `local_revision.go` resolves managed-project worktrees (config coupling). Mirror/runner/history/worktree-status machinery is Git-generic. | GTW-name **Yes** (partial); Hub-auth **No**; PLAW **No**; migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **git**. |
| `internal/hub` | 9/5 | config, fsutil, lockfile, model, runtime_log | 7 | `git` executable | `hub_types.go:11` `ProtocolRoot = "gpt-tunnel/v1"` — the remote on-disk layout version; the package owns remote **authority semantics**, not just naming — see §4. `git_repository`, `snapshot`, `backup`, `transaction`, `write`, `read`, `ensure`, `remove` machinery is Git-CAS-generic and separable from the authority model. | GTW-name **Yes**; Hub-auth **Yes** (it *is* the remote layer — see §4); PLAW **No**; migrate **No**; debug **No**; callback **No**; Task-era **No**; runtime-dep **git**. |
| `internal/model` | 47/26 | workflowrole | 1 | none | `agent.go`→`workflowrole` = the sole PLAW edge under model; `e2e_procedure.go`, `activate_local_procedure.go`, `activation_preflight_procedure.go`, `release_prod_procedure.go` define GTW-authored procedures bound to GTW scripts; `operator_journal*`, `orphan_run_recovery`, `task_revision*` carry GTW/legacy-era types; `model_identifiers*` encode GTW key formats (`GTW-TSK*` family). Core semantic types/validators/hashing are the highest-value extract set. | GTW-name **Yes**; Hub-auth **No** (types only); PLAW **Yes** (via agent.go); migrate **Yes** (legacy-decode types); debug **No**; callback **Yes** (`project_callbacks.go`); Task-era **Yes**; runtime-dep **No**. |
| `internal/sqlitestore` | 75/39 | model, pagination | 3 | direct ext `go-sqlite-store/{migrate,store}` → CGO libsqlite3 + pkg-config | **36 `*migration*.go` files** (databases_*_migration, project_configuration_*_migration, tsk384/409/480/531/620/623-era, shared_sequence_hard_cut, shared_*_baseline) exist only to evolve GTW databases. **Correction per MSG16:** the `migrate` import is *not* historical-only — `shared_lifecycle_event.go:14` imports `go-sqlite-store/migrate` and `sharedLifecycleEventMigration` (`:23-45`) constructs **current** CREATE TABLE/INDEX DDL; `databases_{shared,local}_baseline.go` import `migrate` for baseline schema construction. Schema definition is mixed into core read/write code — dropping `*migration*` filenames alone cannot yield a compile-closed clean schema; retained tables' schema definitions/bootstrap plumbing must be extracted/re-authored before the `migrate` edge can close. Core `databases*.go`, `shared_lifecycle*` (entity-neutral sequence/history/event/status/conflict machinery), `shared_mutation*` (CAS+outbox atomicity), `shared_relations`, `task_execution*`, `local_*` are high-value. | GTW-name **Yes**; Hub-auth **Yes** (outbox→remote publication); PLAW **Yes** (`plaw_messages.go` filename); migrate **Yes** (dominant); debug **No**; callback **Yes** (`callback_epochs.go`); Task-era **Yes**; runtime-dep **CGO/libsqlite3**. |
| `internal/service` | 163/121 (120 active + 1 `liveperformance`) | actioncontract, activation, airelay, authority, config, controller, entity, fsutil, gates, gitx, hub, lockfile, model, pagination, publicprojection, runtime_log, session, sqlitestore, tokenizer, workflowrole | 23 | direct ext `go-sqlite-store/store` → CGO libsqlite3; runtime: `git`, `python3` (procedures), `airelay` (via airelay pkg), `systemd` (via controller) — invoked only on their feature paths | Two populations (evidence: file-name census): **semantic lifecycle** (~70 files: `task_authoring*`, `task_lifecycle*`, `task_complete.go`, `track_lifecycle.go`, `milestone_*`, `journal_*`, `shared_*`, `relation.go`, `adr_*`, `workflow_policy*`, `task_execution_{lifecycle,integrate,verification,review,state}`, `project_configuration*`, `procedure_execution.go`, `durable_*`, `liveness_*`, `entity_registry`, `state_contract`) and **host/Task-era scaffolding** (~90 files: `agent_*`×18, `session_*`, `admin_*`, `callback_*`, `bootstrap`, `hotfix`, `cutover`, `operator_*`, `journal_migration`, `project_configuration_hub_migration`, `state_repair`). 29 prod files synchronously call `s.Hub.*` (see §4). Extract/rehome only. | GTW-name **Yes**; Hub-auth **Yes**; PLAW **Yes**; migrate **Yes**; debug **Yes** (repair/debug); callback **Yes**; Task-era **Yes**; runtime-dep **CGO/libsqlite3, python3**. |
| `internal/mcp` | 85/94 (93 active + 1 `liveperformance`) | actioncontract, agentguide, airelay, authority, config, controller, debug, hub, mcpmanifest, model, pagination, publicprojection, runtime_log, service, session, sqlitestore, tokenizer (17) | 26 | direct ext none; transitive modules reach yaml/tiktoken/uuid/regexp2/go-sqlite-store via those edges; runtime deps via owned features (git, airelay, systemd, python3, CGO) — graph reach only | MCP transport/schema-validation/compact-projection machinery is reusable; the action inventory (`generic_*_actions.go`×~30) encodes the GTW entity/PLAW corpus; `debug_actions.go`, `agent_canonical.go`, `agent_cli_submit.go`, `operator_*`, `mcp7_*` bind undecided surfaces. Rebuild action surface on kept primitives. | GTW-name **Yes**; Hub-auth **Yes**; PLAW **Yes**; migrate **Yes** (legacy envelopes); debug **Yes**; callback **Yes**; Task-era **Yes**; runtime-dep **No** (loopback HTTP). |
| `internal/session` | 5/8 | model, sqlitestore, workflowrole | 4 | CGO libsqlite3 (transitive) | `roles.go` re-exports `workflowrole` — planner/lead/advisor/worker codes P/L/A/W + `airelay_session_key` ref semantics; `store*.go` durable session machinery is reusable. Role registry must be re-authored. | GTW-name **Yes**; Hub-auth **No**; PLAW **Yes**; migrate **Yes** (legacy payload/IDs); debug **No**; callback **No**; Task-era **Yes**; runtime-dep **CGO (transitive)**. |
| `internal/workflowrole` | 1/0 | none | 0 | none | `roles.go` (97 lines, not ~40 — corrected) is the fixed PLAW registry + `airelay_session_key` ref semantics — the single role-model decision point. | GTW-name **No**; Hub-auth **No**; PLAW **Yes** (definition site); migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/authority` | 1/1 | session | 5 | CGO (transitive) | `Require*Role` helpers enforce the PLAW set — small file, role-model-bound. | GTW-name **No**; Hub-auth **No**; PLAW **Yes**; migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **CGO (transitive)**. |
| `internal/config` | 9/6 | fsutil, lockfile | 2 | none | `managed_projects_*` registry + atomic digest store reusable; `config.go` field set, `DefaultPath`, `airelay_session_key` binding semantics, hub-checkout/managed-hub fields are GTW-shaped. | GTW-name **Yes**; Hub-auth **Yes** (hub config fields); PLAW **Yes** (binding refs); migrate **Yes** (legacy binding discard); debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/gates` | 5/4 | model, tokenizer | 3 | none (exec via caller) | `gates_gate_token*.go` gate-token machinery + `test_scope.go` reverse-transitive package-graph + `package_graph.go`; gate vocabulary/standard-gate set is GTW policy. `gates.go` is a 1-line stub. | GTW-name **Yes**; Hub-auth **No**; PLAW **No**; migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/agentguide` | 1/0 | none | 0 | none | `Content` struct + embedded GTW supervision prose (role/delegation/attach instructions). Struct shape reusable; all content re-authored. | GTW-name **Yes**; Hub-auth **No**; PLAW **Yes**; migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/mcpmanifest` | 1/0 | none | 0 | none | Canonical tool-name inventory (6 tools) — GTW surface; trivial file. | GTW-name **Yes**; Hub-auth **No**; PLAW **No**; migrate **No**; debug **No**; callback **No**; Task-era **No**; runtime-dep **No**. |
| `internal/releaseartifacts` | 1/1 | none | 0 | none | Embedded-SHA binary-source-revision + artifact replace/snapshot/restore — generic artifact-integrity primitives bound to the GTW release pipeline. | GTW-name **Yes**; Hub-auth **No**; PLAW **No**; migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `internal/testutil` | 2/0 | config, sqlitestore | 7 | `go-sqlite-store/store` → CGO | Test fixtures (bare-remote repo + seeded service). Travels only with test harness rework. | GTW-name **Yes**; Hub-auth **Yes** (fixture shape); PLAW **Yes** (fixture roles); migrate **No**; debug **No**; callback **No**; Task-era **Yes**; runtime-dep **CGO**. |
| `cmd/gpt-tunnel` | 20/23 | agentguide, config, controller, gates, gitx, model, publicprojection, releaseartifacts, service (9) | 25 | direct ext none; transitive modules yaml/tiktoken/go-sqlite-store/uuid/regexp2; runtime: git, systemd+CGO via owned paths — graph reach only | Operator/agent CLI — command inventory + `gpt-tunnel` naming + session-attach/task/track/hub routes. Arg-parse/dispatch patterns reusable; surface re-authored. | GTW-name **Yes**; Hub-auth **Yes**; PLAW **Yes**; migrate **Yes** (retired-route handling); debug **Yes**; callback **No**; Task-era **Yes**; runtime-dep **No**. |
| `cmd/gpt-tunnel-gatewayd` | 1/7 (2 active + 5 `livee2e`) | authority, config, controller, debug, hub, mcp, model, releaseartifacts, service, session, sqlitestore (11) | 27 | direct ext none; transitive all 5 external modules; runtime: CGO/libsqlite3, git, airelay, systemd, python3 — graph reach only | Daemon entrypoint — wiring config→stores→service→mcp. Entrypoint shape reusable; startup/reconcile wiring GTW-specific. | GTW-name **Yes**; Hub-auth **Yes**; PLAW **Yes**; migrate **Yes** (bootstrap migration defer); debug **Yes**; callback **No**; Task-era **Yes**; runtime-dep **CGO**. |
| `contracts` | 1/0 | none | 0 | none | `assets.go` embeds `actions.yaml`+`shared-definitions.yaml`. Embed plumbing generic; the YAML corpus (148 actions, GTW shared definitions incl. `gtw-timestamp` format at `shared-definitions.yaml:109`) is GTW data — corpus rewritten, so this is **not** a whole-package keep. | GTW-name **Yes** (data); Hub-auth **Yes** (entity families); PLAW **Yes** (role refs); migrate **Yes** (retired-domain history); debug **No**; callback **Yes**; Task-era **Yes**; runtime-dep **No**. |

### 2.3 Runtime/host packages — audited, not blanket-dropped (per MSG15 §3)

JRN42 decided not to copy runtime **state**; it did not decide that all process/agent/runtime
**code** is out of scope for a Gateway product. These packages are classified on whether they
contain generic, extractable primitives versus pure GTW-hosting coupling. Feature-level adoption
(daemon control, self-upgrade, agent relay, debug surface) is **undecided** and labeled so.

| Package | Prod/Test | Class | Direct internal deps | Direct external / transitive modules | Runtime deps (graph reach — exercised only on the feature's path) | Coupling axes | Evidence & extract/rehome candidates |
|---|---|---|---|---|---|---|---|
| `internal/airelay` | 6/6 | **REWORK_CANDIDATE** | session | none / `go-sqlite-store`+`model`-tree transitives | `airelay` binary | GTW-name **Yes** (session-key semantics); Hub-auth **No**; PLAW **Yes** (session roles); migrate **Yes** (legacy validation per `DeriveExecutionSessionKey` doc); debug **No**; callback **Yes** (control channel); Task-era **Yes** | Channel itself (external `airelay` binary + Devin sessions) is host/vendor-specific — feature adoption **undecided**. Generic subprocess-boundary primitives with proof value: fixed arg-vector construction, bounded child output, UTF-8 byte bound, deadline handling (`client_airelay_{ensure,launch,status,types}.go`, proven by `client_tests_*`); `DeriveExecutionSessionKey` pure/deterministic. Extract/rehome: subprocess-boundary helpers. |
| `internal/controller` | 18/13 | **REWORK_CANDIDATE** | config, fsutil, lockfile, releaseartifacts, runtime_log | none / none | `git`, `systemd`, process signals | GTW-name **Yes**; Hub-auth **No**; PLAW **No**; migrate **Yes** (activation/recovery); debug **Yes**; callback **No**; Task-era **Yes** (hosting) | Daemon lifecycle/systemd/upgrade/recovery is GTW-hosting-coupled — feature adoption **undecided**. Extractable generic primitives with unique proofs: `process_identity.go` (identity survives atomic binary replace), `endpoint_ownership.go` (owner-proof + stale-orphan retire + foreign-config fail-closed), `gateway_recovery*.go` (idempotent restart, terminal-receipt reuse, response-release deferral), `gateway_activation.go` (durable snapshot/rollback ordering), `environment.go` (secrets-binding/open-permission rejection). |
| `internal/upgrade` | 11/11 | **REWORK_CANDIDATE** | activation, config, controller, fsutil, gitx, lockfile, releaseartifacts, service | none / all 5 external modules | `git`, `systemd`, CGO (via service/sqlitestore reach) | GTW-name **Yes**; Hub-auth **Yes** (`inspect_hub_revision`); PLAW **No**; migrate **Yes**; debug **Yes** (diagnostics); callback **No**; Task-era **Yes** | Self-upgrade pipeline is GTW-daemon-bound — feature adoption **undecided**. Extractable: atomic multi-file replace with per-position restore (`upgrade_artifacts.go`/`upgrade_runner.go` proofs), rollback proof-closure + cleanup-failure backup retention, corrupt/symlink/noncanonical record rejection (`status.go`), sanitized bounded diagnostics. |
| `internal/activation` | 6/9 | **REWORK_CANDIDATE** | config, controller, fsutil, mcpmanifest, releaseartifacts, sqlitestore | none / `go-sqlite-store` | CGO (via sqlitestore), `git` (via controller) | GTW-name **Yes**; Hub-auth **Yes** (candidate checks); PLAW **No**; migrate **Yes**; debug **Yes**; callback **No**; Task-era **Yes** | GTW activate-local/candidate lifecycle is hosting-bound — feature adoption **undecided**. Extractable: `runBoundedCommand` bounded subprocess execution + deterministic truncation (`activate_runtime_file.go:347`; proofs in `activate_test.go`), `recovery_snapshot.go` (artifact+state snapshot/restore + corrupt-reject), `sha256File` artifact digest (`activate_runtime_file.go:119`; `proof_test.go` proofs). |
| `internal/debug` | 2/3 | **REWORK_CANDIDATE** | activation, config, controller, fsutil, lockfile | none / `go-sqlite-store` | `git`, `systemd`, CGO (graph reach) | GTW-name **Yes**; Hub-auth **No**; PLAW **No**; migrate **Yes**; debug **Yes** (the surface itself); callback **No**; Task-era **Yes** | Debug-status surface — feature adoption **undecided**. Extractable: bounded retry with outcome-stickiness (`activation.go`/`activation_retry` proofs), bounded git-output helper (`status.go`, fails-closed at bound + honors deadline). Small package; the primitives could equally be rewritten — UNCLEAR-adjacent, kept REWORK for the two named proofs. |
| `internal/callbackdelivery` | 1/1 | **DROP_CANDIDATE** | none | none / none | none | GTW-name **No**; Hub-auth **No**; PLAW **No**; migrate **No**; debug **No**; callback **Yes** (name only); Task-era **Yes** | `delivery.go` is a one-line `package callbackdelivery` stub; test file has zero `Test` functions. No production content. |
| `cmd/gpt-tunnelctl` | 5/2 | **UNCLEAR** | activation, config, controller, fsutil, releaseartifacts, service, upgrade | none / all 5 external modules | CGO, git, systemd (graph reach) | GTW-name **Yes**; Hub-auth **Yes** (via upgrade/service reach); PLAW **Yes** (via service reach); migrate **Yes**; debug **Yes**; callback **No**; Task-era **Yes** | Daemon-control/upgrade CLI bound to the undecided daemon-control + self-upgrade features. Value conditional on RSG adopting those surfaces; not importable standalone. |

Package-row totals: **KEEP 8 / REWORK 26 / DROP 1 / UNCLEAR 1 = 36.**

## 3. Non-Go surfaces

| Surface | Files | Class | Evidence |
|---|---|---|---|
| Test harness: `test-fast.py`, `test-full.py`, `test-full.sh`, `test-e2e.sh`, `test-race.sh`, `test-performance.py`, `test-profile.py`, `smoke_mcp.py` | 8 | REWORK_CANDIDATE | Harness shape reusable; module paths/repo names GTW-bound. |
| Static gates: `static-check.py`, `check-go-format.sh` | 2 | REWORK_CANDIDATE | Policy gates contain GTW inventory lists/module-path literals (`gpt-tunnel-gateway` module checks) — mechanism portable, embedded inventories re-authored; **not** a clean KEEP. |
| `task-verify.py` | 1 | REWORK_CANDIDATE | Canonical verify-gate script; gate vocabulary re-authored. |
| Release group: `release.py`, `release-prod.py`, `release_prod_test.py`, `build-release.sh`, `check-github-ci.py`, `github_tooling.py`, `post-integrate.py`, `pre-integrate.py`, `validate-release-tool-conformance.py`, `verify-release-publication.py` + `release_tooling/` (10 modules) | 10 top-level + 10 modules | UNCLEAR | Release pipeline — RSG pipeline undecided; conditional import at most. |
| Activation group: `activate-local.py`, `activate_local_test.py`, `activation-preflight.py`, `integration_activate.py`, `integration_activate_test.py`, `upgrade-bootstrap.sh`, `upgrade_rehearsal.py`, `test-integration-activate.sh` | 8 | UNCLEAR | Bound to undecided activation/upgrade features. |
| **Scripts total** | **29 + 10 = 39** | | Groups: harness 8 + static 2 + task-verify 1 + release 10(+10) + activation 8 = 29 top-level. |
| `contracts/{actions,shared-definitions}.yaml` | 2 (148 actions) | REWORK_CANDIDATE | Canonical action corpus — GTW entity/action/role vocabulary; RSG corpus rewritten. |
| `schemas/*.schema.json` | 14 | REWORK_CANDIDATE | `adr`/`plan`/`project*`/`task*` document schemas portable-ish; `run*`/`report*`/`operator-journal*`/`gpt-tunnel-completion` GTW-specific. |
| `procedures/e2e.py` | 1 | REWORK_CANDIDATE | Envelope-unwrap/fail-closed/loopback/snapshot-binding procedure pattern — strong template; bound to GTW actions + planner role. |
| `fixtures/*.json` | 7 | DROP_CANDIDATE | Historical document fixtures for legacy readers RSG lacks; conditional KEEP only where a kept validator uses them as oracle data. |
| `docs/*.md` | 23 | not-source | `ARCHITECTURE.md`, `MCP_CONTRACT.md`, `HUB_LAYOUT.md`, `CANONICAL_AGENT_TOOLING.md`, `BEHAVIOR_CONTRACT.md`, `TEST_GATE_SEPARATION.md` are design-input reading; runbooks are GTW-named artifacts. |

## 4. Remote-authority (Hub) coupling — concrete map

Per MSG15 §2: Hub-first authority is **not** naming-only. JRN42 separates the remote *State
Repository* (user-owned Git) from active local *Shared State* with **explicit restore**. The
concrete coupling:

**Synchronous remote-as-authority call sites** — `grep -l 's\.Hub\.\|hub\.{Write,Transact,Read,Ensure,Snapshot}'`
over non-test files returns exactly **29 files**: 28 in `internal/service/` —
`service_task_state.go` (10 call sites), `service_plan_sections.go` (9),
`task_authoring_mutation.go` (8), `project_update.go` (8), `project_retirement.go` (7),
`task_authoring.go` (5), `service_projects.go` (5), `service_project_mutations.go` (5),
`shared_outbox_track_reconciliation.go` (5), `plan_cutover.go` (4), `shared_rule.go` (4),
`service_task_mutations.go` (4), `service_project_status_plan.go` (4), `service_creation.go` (4),
`adr_revisioned_mutations.go` (4), `task_revision_read.go` (3),
`task_revision_legacy_evidence.go` (3), `project_configuration{,_api,_hub_migration,_shared}.go` (4 files),
`workflow_policy{,_mutation}.go` (2 files), `bootstrap.go`, `service_plan_adr.go`,
`service_completion_types.go`, `shared_outbox_worker.go` (18 — async publication),
`shared_restore.go` (5) — plus `internal/mcp/generic_transport_dispatch_dispatch_registry.go`.
Representative shapes:
- `service_task_state.go:66,129` — `s.Hub.Transact(ctx, in.ExpectedHubRevision, subject, fn)` — **CAS on remote revision inside a synchronous mutation path** (legacy task-state family; `task_revision_*` legacy surface reads the same family).
- `task_authoring.go:38,65,133` — `s.Hub.ReadJSON`/`Transact` for task reads/creates (hub-first task surface; current Shared-first task paths exist alongside — `task_authoring_shared_tests_*` prove shared-read authority, so **both flows coexist**).
- `bootstrap.go:99` `s.Hub.RemoteRevision`, `:120-124` `rollbackOnboardHub`, `:359` `restoreHubProjectSemantics`, `:262` `s.Hub.ReadFile` — **onboard/adopt is a synchronous hub transaction + compensation flow**.
- `shared_restore.go:52,152,199,264,304` — `restoreHub*` walks a `hub.ReadSnapshot` (families: entities, revisions, lifecycle events, canonical sequences) hydrating local Shared — this is the restore direction RSG's explicit-restore model needs, bound to `hub.ReadSnapshot`'s on-remote layout.
- `shared_outbox_worker.go` — async publication: drains `shared_mutation` outbox and `hub.WriteJSON`s canonical records per family — **the publication direction** (local authority → remote mirror).

**Two separable assets:** (a) *Git mechanics* — `hub.Transaction` (CAS-on-remote-revision with
expected-revision), `WriteJSON` bounded path writes, `ReadSnapshot` one-revision consistent
reads, `Ensure` managed-clone lifecycle, lock attribution — proven by `hub_tests_*` and reusable
for any Git-backed remote; (b) *authority/control flow* — which side is authoritative when
(currently split: synchronous hub-CAS on legacy surfaces vs shared-first+outbox on current
surfaces), `ExpectedHubRevision` protocol, adopt/rollback compensation, and the `gpt-tunnel/v1`
on-remote layout. RSG's authority model is undecided — importing (a) does not import (b).

**Do not mint a protocol root/schema for RSG here** — `ProtocolRoot = "gpt-tunnel/v1"`
(`hub/hub_types.go:11`) and `entity/registry.go:16` `protocolRoot = "gpt-tunnel/v1/projects"`
are GTW versioned-layout constants; any RSG layout is a new decision, and inheriting the string
would falsely claim format compatibility.

## 5. Path-level ownership map

| Concern | Owning files | Disposition |
|---|---|---|
| Schema/contract compiler | `internal/actioncontract/*`, `contracts/*.yaml`, `internal/mcp/action_contracts.go`, `schema*.go` | Engine KEEP-under-corpus-retarget; corpus REWORK; mcp wiring REWORK. |
| Durable storage | `sqlitestore/{databases,shared_lifecycle*,shared_mutation*,shared_relations,task_execution*,task_completion,local_*}.go` + `go-sqlite-store` (CGO) | Core REWORK/KEEP; **36 `*migration*.go` + `*_baseline*.go`** DROP on clean schema (bounded-migration *mechanism* proofs reharnessed — §7). |
| Git layer | `gitx/*` + `hub/*` | Mechanics KEEP/REWORK; authority semantics §4. |
| Process/runtime | `controller`, `upgrade`, `activation`, `debug`, `cmd/gpt-tunnelctl` | Feature adoption undecided; named extractable primitives in §2.3. |
| Auth/session/agent | `session`, `authority`, `workflowrole`, `service/{agent_*,session_*}`, `airelay`, `callbackdelivery` | Store machinery reusable; role registry re-authored; airelay feature undecided; callbackdelivery stub DROP. |
| Bootstrap/restore/publication | `service/{bootstrap,shared_restore,shared_outbox_*,project_update,tsk692-adjacent}.go`, `hub/{ensure,snapshot,backup,transaction,write,read}.go` | The JRN42 dogfood path (remote→explicit restore→local authority) — mechanics reusable; bootstrap/adopt control flow re-authored. |
| Verification | `gates`, `service/{service_gates*,task_execution_verification,verify,procedure_execution,test_gate_receipt*}.go`, `scripts/task-verify.py`, `procedures/e2e.py` | Proof model (immutable receipts, gate profiles, procedure sandbox) — mechanism reusable; vocabulary re-authored. |
| MCP transport | `mcp/{server*,schema*,compact_projections*,generic_transport*,generic_system_await}.go` | Transport/schema machinery reusable; `generic_*_actions` corpus REWORK; debug/agent/operator surfaces undecided. |
| Messaging | `service/message_lifecycle.go`, `sqlitestore/plaw_messages.go` | Message lifecycle semantic; PLAW routing re-authored. |

## 6. Proposed initial RSG seed closure (nonnormative)

**Feasibility label: proposed, unimplemented.** This is one coherent candidate set — not an
import boundary decision.

### 6.1 WHOLE-package candidates (8 packages, per §2.1 KEEP definition)

`internal/fsutil`, `internal/lockfile`, `internal/pagination`, `internal/tailcursor`,
`internal/runtime_log`, `internal/gofmtstruct` (+`cmd/gofmt-struct`), `internal/tokenizer`.

- Original reachable internal union of this set (frozen `go list -deps`): **`{fsutil, lockfile,
  pagination, tailcursor, runtime_log, gofmtstruct, tokenizer}`** — self-closed; adds nothing.
- External deps: `tiktoken-go` (+ transitive `regexp2`, `uuid`) — offline-embedded tokenizer.
- Runtime deps: none.

### 6.2 EXTRACT/REHOME candidates (file/symbol level — decoupling required, never whole-package)

| From | Extract set | Decoupling required (each edge explained) |
|---|---|---|
| `internal/model` | semantic types/validators/hashing for adopted families | Sever `agent.go`→`workflowrole` (only internal edge — isolated: `grep -rln workflowrole internal/model` = `agent.go` only); drop/rehome `e2e_procedure`, `activate_local_procedure`, `activation_preflight_procedure`, `release_prod_procedure` (GTW procedure defs); `operator_journal*`/`orphan_run_recovery`/`task_revision*` conditional on document/legacy adoption. |
| `internal/sqlitestore` | `databases*`, `shared_lifecycle*` (11), `shared_mutation*` (4), `shared_relations`, `task_execution*` non-migration, `task_completion`, `local_*`, `*_lifecycle_policy`, `shared_sequence_reconstruction` | Drop historical-evolution migrations (36 `*migration*.go` bodies) — **but schema DDL is mixed into kept files**: `shared_lifecycle_event.go:14` imports `migrate`, `sharedLifecycleEventMigration` (`:23-45`) builds current CREATE TABLE/INDEX; `databases_{shared,local}_baseline.go` build the baseline via `migrate`. Retained tables need their schema definitions/bootstrap plumbing rehomed/re-authored — the `go-sqlite-store/migrate` edge closes only after that extraction (unimplemented); `plaw_messages.go` rename+role decoupling; `callback_epochs` conditional on callback model. |
| `internal/service` | semantic-lifecycle population (~70 files listed §2.2) | Excise ~90 host/Task-era files; every imported file re-audited for `s.Hub.*` (§4 map), `airelay`, `session`-role, `agent_*` edges. Feasibility: large manual boundary work — **unproven until the file-level list is ratified**. |
| `internal/gitx` | runner/types/history/mirror/default_branch/repository/push/commit_tree/worktree status | `task_worktree*`/`onboard.go`/`task_integration.go`/`local_revision.go` stay behind or re-author (Task-era naming + config coupling). |
| `internal/hub` | `git_repository`, `snapshot`, `backup`, `transaction`, `write`, `read`, `ensure`, `remove`, types | §4(b) authority semantics excluded; `ProtocolRoot`/`hub_types` constants re-authored (not copied). |
| `internal/entity` | registry machinery | `protocolRoot` + descriptor set re-authored. |
| `internal/actioncontract` | compiler/validator/invariants engine | Corpus binding re-pointed; `requiredDefinitions`/retired lists/`gtw-timestamp` re-authored. |
| `internal/publicprojection` | projection machinery | GTW action IDs + semantic field omissions re-authored. |
| `internal/mcp` | `server*`, `schema*`, `compact_projections*`, `generic_transport*`, `generic_system_await` machinery | `generic_*_actions` rebuilt on RSG corpus; debug/agent/operator/admin surfaces undecided. |
| `internal/session` | `store*.go` | `roles.go` re-authored (workflowrole severed). |
| `internal/config` | `managed_projects_*` registry + validation shape | GTW field set/`airelay_session_key` semantics dropped. |
| `internal/authority`, `internal/workflowrole`, `internal/agentguide`, `internal/mcpmanifest`, `internal/releaseartifacts`, `internal/gates`, `internal/testutil` | named helpers/shape | Each is small REWORK — PLAW/GTW vocabulary resolved at authoring. |
| `internal/{airelay,controller,upgrade,activation,debug}` | §2.3 named primitives only | Feature adoption undecided; primitives extracted if their owner surface is rebuilt, not imported wholesale. |

### 6.3 Do-not-import set (evidence-based, conditional — not final decisions)

- `internal/callbackdelivery` — empty stub (evidence: 1-line file, 0 test funcs).
- `internal/service/{agent_*,session_*,admin_*,callback_*,hotfix_*,cutover_*,operator_*}` —
  conditional on agent/role/runtime decisions; not blanket.
- All `internal/**/{*_migration*.go,*_baseline*.go}` — conditional on clean schema (RSG
  explicitly plans clean; mechanism proofs preserved via §7 REWRITE rows).
- `fixtures/*`, GTW-content `*_migration_test` proofs per §7 ledger.
- `internal/model/{orphan_run_recovery,operator_journal*}` — conditional on document-format
  adoption (UNCLEAR).
- Release/activation/upgrade scripts — conditional on those features.

### 6.4 Edge-elimination explanation (per proposed set — labeled feasibility assumptions)

| Proposed eliminated/replaced edge | Basis |
|---|---|
| `service`→`{airelay,controller,upgrade,activation,debug,callback_*}` file population | Scaffolding set per §2.2 census; requires the file-level boundary ratified — **assumption**. |
| `model`→`workflowrole`, `session`→`workflowrole`, `authority`→`session` role edges | Role model is an explicit RSG decision — edges rebuilt, not carried. |
| `sqlitestore`→`go-sqlite-store/migrate` | **Not confined to migration files** — `shared_lifecycle_event.go:14` and `databases_{shared,local}_baseline.go` use `migrate` for *current* schema DDL. Edge closes only after retained-table initializers are extracted/re-authored (unimplemented); `store` stays (CGO/libsqlite3 runtime dep **remains**). |
| `entity`/`hub` `gpt-tunnel/v1*` protocol constants | Versioned-layout constants re-authored, not copied (§4). |
| `actioncontract`→`contracts` GTW corpus | Corpus rewritten; compiler engine unaffected. |
| `mcp`→`{debug,agent_canonical,operator_*}` action surface | Undecided surfaces; transport unaffected. |
| `cmd/gpt-tunnelctl`, `internal/callbackdelivery` | UNCLEAR / empty stub. |

## 7. Naming / boundary observations (corrected)

- `internal/workflowrole/roles.go` is **97 lines** (v1 said ~40 — wrong).
- `internal/service/durableMutationExecutionSet{1,2,3}.go` are **real tracked files**
  (verified `git ls-files` + read; Lead's v2 concern resolved by this evidence — no rename or
  delete requested). CamelCase filenames in a snake_case directory; they contain
  durable-mutation execution dispatch for task-execution submits — a naming inconsistency to
  flag at import.
- `hub.ProtocolRoot = "gpt-tunnel/v1"` (`hub_types.go:11`) and `entity` `protocolRoot =
  "gpt-tunnel/v1/projects"` (`registry.go:16`) — separate versioned-layout constants.
- `internal/callbackdelivery/delivery.go` and `internal/gates/gates.go` are one-line package
  stubs — empty anchors.
- JRN42 vocabulary: remote user-owned Git = *State Repository*; local Git cache = *State
  Mirror*; active local semantic DB = *Shared State*. `internal/hub`, `*_mirror*`, `shared_*`
  naming follows that rename on import.
- Module rename `github.com/rceman/gpt-tunnel-gateway` → `github.com/rceman/reposuite-gateway`
  touches every import path (`internal/*` imports are all module-prefixed).

## 8. Test-proof ledger — one row per test path (401 files, disjoint, exhaustive)

Classification is **proof-based** (what invariant each file proves), not name- or speed-based.
Classes:
- `PRESERVE_INVARIANT` — proves a universal invariant (CAS/atomicity, source+tree identity,
  replay/idempotency, corruption/recovery, bounds, strict decoding, artifact/source identity)
  that transfers with the code modulo rename.
- `REWRITE_FOR_RSG` — proof survives but the harness/identity/vocabulary must be re-targeted
  (role model, action corpus, ID formats, entity families, GTW procedure content).
- `REPLACE_WITH_CONTRACT_HARNESS` — the proof belongs on a rebuilt public/contract surface
  (action corpus, CLI/daemon/public-transport contracts, family-disposition inventory gates);
  imported tests would not compile against a re-authored surface.
- `DROP_LEGACY_COMPATIBILITY` — proof owner is GTW-only: migration-compat content,
  one-time cutover proof, live-GTW-daemon environment proof, empty helper/fixture file
  (zero `Test` functions), or wholly undecided-surface proof with no reusable invariant.
  **Empty/helper files are listed explicitly rather than guessed.**
- `UNCLEAR` — honest open item: proof owner is an undecided surface (agent model, admin/operator
  surface, telemetry, Apps-SDK, plan format, procedure-seed adoption, ctl CLI, performance
  baseline). The unresolved question is stated per row.

Rules applied (per MSG15 §4): every row names a representative `Test*` function as evidence
(extracted mechanically via `func Test\w+` over all 401 paths — no fabricated assertions);
mixed-content files get **one conservative class** with the differing subpart noted in the
rationale; migration tests are classified by their *mechanism* proof (bounded/idempotent/
fail-closed state migration → REWRITE) rather than a clean-history inference; files with zero
`Test` functions are marked as such; build-tag-excluded files carry their tag; no file is
counted twice.

**Totals generated from the rows below:** PRESERVE_INVARIANT **52** | REWRITE_FOR_RSG **218** |
REPLACE_WITH_CONTRACT_HARNESS **25** | DROP_LEGACY_COMPATIBILITY **50** | UNCLEAR **56** |
**total 401** (= 394 active + 7 ignored; per-package sums equal the frozen `TestGoFiles` +
`IgnoredGoFiles` denominators in §1.3).



**`cmd/gofmt-struct`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `main_test.go` | PRESERVE_INVARIANT | `TestRunCheckAndWriteModes` | check/write modes + sibling-file map preservation — formatter invariants |

**`cmd/gpt-tunnel`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `agent_commands_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestAgentTailCLIFailsClosedWithoutLocalStoreOrAirelay` | fail-closed-without-runtime CLI contract — reprove on RSG CLI surface |
| `agent_register_cli_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestAgentRegisterCLIOutputIsBoundedActionProjection` | bounded action-projection output contract |
| `agent_register_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestAgentRegisterCLISyntaxRejectsMalformedArguments` | malformed-arg syntax contract |
| `daemon_contract_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestPublicDaemonSurfaceIsExactlyFourOperations` | public daemon surface = exactly N ops — surface contract |
| `guide_test.go` | REWRITE_FOR_RSG | `TestGuideIsZeroStateAndMatchesCanonicalContent` | guide-content parity + arg rejection — content re-authored, mechanism reused |
| `help_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestHelpQuickStartPrecedesCommandInventoryAndAvoidsStaleOnboardingUX` | help inventory/UX contract |
| `main_test_cli_validation_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestCancelAcknowledgeCLIArgumentsAreStrict` | strict arg validation + routing contract |
| `main_test_registry_test.go` | REWRITE_FOR_RSG | `TestGitcmdResolvesManagedProjectAfterServiceConstruction` | managed-registry resolution + fail-closed malformed registry |
| `main_test_support_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — shared helper file, proof lives in callers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `main_test_task_lifecycle_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTaskDeferCLIRouteIsRetired` | proves retired-CLI-route hard cut for GTW task surface — GTW command inventory |
| `operator_client_test.go` | PRESERVE_INVARIANT | `TestOperatorCLIRequestEnforcesBodyBounds` | request body bounds enforcement — transport-bound invariant |
| `project_onboard_cli_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestProjectOnboardPositionalArgsMapToCanonicalWorkerBinding` | onboard arg-map/bounded-output contract |
| `project_onboard_token_cli_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestProjectOnboardCLIExposesDurablePlannerToken` | durable planner token exposure contract |
| `project_update_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestProjectUpdateCLIRequiresExactArgumentShape` | exact arg-shape contract |
| `session_commands_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestSessionAttachCLIRequestHitsOperatorRouteWithExactIdentity` | session-attach operator-route identity contract |
| `tsk567_task_read_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK567TaskReadUsesAirelayRuntimeAuthority` | task/read runtime-authority contract |
| `tsk640_task_submit_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK640TaskSubmitUsesFixedAgentCLIEndpoints` | submit endpoint/transport contract |
| `tsk654_project_token_live_test.go` | REWRITE_FOR_RSG | `TestTSK654ProjectTokenLiveDaemonCwdResolutionAndDurability` | durable project-token cwd/identity durability proof — disposable testutil.NewLiveGateway harness (not owner prod); retarget harness to RSG daemon |
| `tsk655_durability_inventory_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK665Gate20OperatorCLIDurabilityInventory` | durability-ownership inventory gate — reprove inventory on RSG surface |
| `tsk655_live_gateway_test.go` | REWRITE_FOR_RSG | `TestTSK655LiveGatewayOperatorCLIUsesDaemonOwnedDurability` | daemon-owned durability via CLI — disposable harness; daemon-durability proof re-targeted |
| `tsk657_submit_transport_live_test.go` | REWRITE_FOR_RSG | `TestTSK657LiveSubmitResponseLossReconcilesDurably` | response-loss→repeat reconciles exact durable outcome (Lead-inspected lines 28-129) — admission-reconciliation invariant; disposable harness |
| `tsk659_sequential_submit_live_test.go` | REWRITE_FOR_RSG | `TestTSK659LiveSequentialSubmitAdmissionsAreTaskScoped` | task-scoped sequential admission — admission-scope invariant; disposable harness |
| `tsk667_live_outbox_text_test.go` | REWRITE_FOR_RSG | `TestTSK667LiveGatewayDrainsExistingTextRelationOutbox` | existing-outbox drain/convergence — outbox-drain invariant; legacy-text subpart drops |

**`cmd/gpt-tunnel-gatewayd`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `hub_sync_resilience_test.go` | REWRITE_FOR_RSG | `TestPostReadyHubSyncLoopRetriesTransientStateCheck` | post-ready remote-sync retry/lock-contention/convergence — sync resilience reusable for state-repo publication |
| `main_test.go` | REWRITE_FOR_RSG | `TestHoldHubRepositoryLockHelper` | bootstrap readiness under remote lock/unavailable + degraded-mode behavior — daemon lifecycle proofs under new remote model |
| `runtime_restart_candidate_e2e_test.go *(build-tag livee2e)*` | UNCLEAR | `(build-tag livee2e)` | livee2e candidate-restart harness — depends on undecided candidate-restart feature (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `runtime_restart_candidate_e2e_tests_debug_activation_test.go *(build-tag livee2e)*` | UNCLEAR | `TestCandidateDebugActivateMCPNetworkE2E` | livee2e candidate debug-activate — undecided debug-activation feature |
| `runtime_restart_candidate_e2e_tests_debug_setup_test.go *(build-tag livee2e)*` | UNCLEAR | `(build-tag livee2e)` | livee2e setup helpers — required if restart/debug proofs kept (rehome/rebuild) (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `runtime_restart_candidate_e2e_tests_restart_gate_test.go *(build-tag livee2e)*` | UNCLEAR | `TestCandidateGatewayRestartMCPNetworkE2E` | livee2e restart gate — undecided restart feature |
| `runtime_restart_candidate_e2e_tests_wait_debug_test.go *(build-tag livee2e)*` | UNCLEAR | `(build-tag livee2e)` | livee2e wait/debug — undecided debug feature (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |

**`cmd/gpt-tunnelctl`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `daemon_contract_test.go` | UNCLEAR | `TestCtlLifecycleBoundaryKeepsOnlyHiddenDaemonPrimitives` | hidden daemon-lifecycle boundary contract — value depends on undecided RSG daemon-control surface |
| `main_test.go` | UNCLEAR | `TestUpgradeResultSelection` | upgrade-dispatch arg parsing — owner undecided |

**`internal/actioncontract`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `compiler_test.go` | PRESERVE_INVARIANT | `TestCompositionOnlyObjectDelegatesPropertiesToBranches` | composition/canonical-tree/conformance/fail-closed/retired-path rejection — compiler invariants (fixture corpus re-points) |
| `procedure_test.go` | PRESERVE_INVARIANT | `TestDynamicProcedureActionCompilesAgainstCanonicalReferences` | dynamic-procedure compilation against canonical references + path rejection — mechanism invariant; gate IDs are corpus data |

**`internal/activation`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `activate_local_procedure_test.go` | UNCLEAR | `TestTSK606ActivateLocalRejectsMismatchedAuthorityBeforeMutation` | authority-before-mutation + canonical-definition proofs — depends on undecided procedure-seed adoption |
| `activate_test.go` | REWRITE_FOR_RSG | `TestRunBoundedCommandCapsCombinedOutput` | TestRunBoundedCommand*/TestBoundedDiagnostic* = generic bounded-subprocess invariants; live-MCP smoke parts drop with owner |
| `activation_preflight_procedure_test.go` | UNCLEAR | `TestTSK627ActivationPreflightScriptEndToEnd` | preflight e2e/stale/dirty rejection — depends on undecided activation feature |
| `candidate_migration_preflight_test.go` | UNCLEAR | `TestCandidatePreflightRejectsTSK602LegacyStateBeforeCutover` | pre-cutover rejection without live mutation — depends on undecided candidate-migration feature |
| `candidate_smoke_test.go` | UNCLEAR | `TestSmokeCandidateReachesHTTPReadyWithinExistingDeadline` | candidate HTTP-ready smoke — depends on undecided candidate-activation feature |
| `debug_test.go` | UNCLEAR | `TestDebugActivateRequiresConfiguredMainBranchBeforeMutation` | debug-activation exact-provenance/auth — depends on undecided debug-activation feature |
| `proof_test.go` | REWRITE_FOR_RSG | `TestProveSourceRejectsInvalidSourceBeforeAnyActivation` | SHA256File exact-artifact digest + prove-source rejection — artifact-integrity invariant |
| `recovery_snapshot_test.go` | REWRITE_FOR_RSG | `TestRecoverySnapshotRestoresMatchingArtifactsAndDurableState` | snapshot restores matching artifacts+durable state, rejects corrupt previous — generic recovery invariant |
| `release_prod_procedure_test.go` | UNCLEAR | `TestTSK529ReleaseProdRejectsMismatchedAuthorityBeforeSideEffect` | authority-before-side-effect + canonical definition — undecided release-procedure surface |

**`internal/airelay`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `client_airelay_ensure_test.go` | REWRITE_FOR_RSG | `TestEnsureDetachedStartsOnceThenReusesHealthyWorker` | detached-worker once/reuse/recover/conflict + owned-stop — process-supervision invariants |
| `client_airelay_validation_test.go` | REWRITE_FOR_RSG | `TestDeriveExecutionSessionKeyRemainsPureAndDeterministic` | pure deterministic session-key derivation + exact-lane validation |
| `client_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — fixture helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `client_tests_status_fixture_test.go` | REWRITE_FOR_RSG | `TestPromptUsesFixedArgumentVector` | fixed arg-vector, bounded child output, deadline, UTF-8 byte bound — subprocess-boundary invariants |
| `client_tests_status_session_test.go` | REWRITE_FOR_RSG | `TestStatusPreservesNonZeroExitAsErrorState` | nonzero-exit-as-error + tail timeout/oversize rejection — subprocess-boundary invariants |
| `client_tests_tail_status_fixture_test.go` | REWRITE_FOR_RSG | `TestTailUsesExactArgumentsAndNormalizesFixture` | exact args, bounded reads, status parse — subprocess-boundary invariants |

**`internal/authority`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `authority_test.go` | REWRITE_FOR_RSG | `TestPlannerOrManagedRuntimeBootstrapDoesNotBecomeRoleAuthority` | bootstrap-session authority narrowness + trusted-root requirement — role-authority invariants under new role model |

**`internal/callbackdelivery`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `delivery_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | empty package (stub) — no proof (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |

**`internal/config`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `config_test.go` | REWRITE_FOR_RSG | `TestLoadRejectsNonLoopback` | nonloopback rejection + config validation — config-safety invariants, GTW field set re-authored |
| `managed_projects_effective_test.go` | REWRITE_FOR_RSG | `TestEffectiveProjectsDerivesManagedMirrorsAndReturnsFreshMap` | effective-project derivation + cross-source collision/fresh-map — registry semantics under new config model |
| `managed_projects_json_test.go` | PRESERVE_INVARIANT | `TestManagedProjectRegistryNestedDuplicateAndTrailingFieldsRejected` | strict decode rejects nested/duplicate/trailing fields — strict-decode invariant |
| `managed_projects_schema_test.go` | PRESERVE_INVARIANT | `TestManagedProjectRegistryAbsentDigestAndPaths` | absent-digest/null rejection, canonicalization, integer-JSON parity — registry schema invariants |
| `managed_projects_storage_test.go` | PRESERVE_INVARIANT | `TestManagedProjectRegistryWriterUsesDigestRevisionAndAtomicState` | digest-revision atomic store + busy-lock/max-revision rejection — CAS/atomicity invariant |
| `tsk552_legacy_binding_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK552LegacyAgentProfileIsDiscardedOnLoad` | legacy agent-profile discard — GTW legacy binding compat |

**`internal/controller`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `controller_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `controller_tests_gate_read_test.go` | REWRITE_FOR_RSG | `TestLogsReadsStructuredRuntimeSource` | structured runtime-log read, bounded/sanitized delta, activate ordering+rollback — log-bound + activation-ordering invariants |
| `controller_tests_process_test.go` | REWRITE_FOR_RSG | `TestProcessIdentitySurvivesAtomicBinaryReplacement` | process identity survives atomic binary replacement — process-identity invariant |
| `endpoint_ownership_test.go` | REWRITE_FOR_RSG | `TestMain` | endpoint-owner proof, stale-orphan retire, foreign-config fail-closed — ownership invariants |
| `env_test.go` | REWRITE_FOR_RSG | `TestReadTunnelEnvRequiresSecretsAndRejectsControllerBindings` | secrets binding rejection, open-permissions reject, env isolation, canonical ready-URL — security-boundary invariants |
| `gateway_activation_durable_test.go` | REWRITE_FOR_RSG | `TestActivateGatewayRestoresMatchingDurableStateAndArtifacts` | durable snapshot/rollback semantics — durable-recovery invariants |
| `gateway_only_lifecycle_test.go` | REWRITE_FOR_RSG | `TestGatewayOnlyLifecycleHelper` | start/stop serialization + one-managed-process isolation — supervision invariants |
| `gateway_recovery_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `gateway_recovery_tests_gate_restart_core_test.go` | REWRITE_FOR_RSG | `TestGatewayRecoveryDuplicateOperationRestartsOneRealProcess` | duplicate-operation restarts one real process — idempotent-restart invariant |
| `gateway_recovery_tests_gate_restart_test.go` | REWRITE_FOR_RSG | `TestAcceptGatewayRecoveryDefersWorkerUntilResponseRelease` | response-release deferral + terminal-receipt reuse + once-only restart — idempotency/recovery invariants |
| `runtime_identity_test.go` | REWRITE_FOR_RSG | `TestCollectRuntimeIdentityMatchesRunningAndInstalledArtifacts` | runtime artifact matching + stale/incomplete rejection — artifact-identity invariant |
| `status_test.go` | UNCLEAR | `TestRunningVersionUsesServerOwnedInitialize` | server-owned initialize — undecided daemon-control surface |
| `systemd_daemon_test.go` | UNCLEAR | `TestDaemonUnitIsCanonicalSystemGatewayAndTunnelService` | canonical unit/path — undecided systemd packaging surface |

**`internal/debug`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `activation_retry_test.go` | REWRITE_FOR_RSG | `TestTSK679PreCutoverFailureAdmitsBoundedRetry` | bounded retry + outcome-stickiness — bounded-retry invariant (owner surface undecided) |
| `activation_test.go` | REWRITE_FOR_RSG | `TestAcceptActivationPersistsBoundedReceiptBeforeWorkerRelease` | durable receipt persist/idempotent terminal + bounded context — durable-receipt invariants |
| `status_test.go` | REWRITE_FOR_RSG | `TestGitOutputFailsClosedAtOutputBound` | git output fails closed at bound + honors deadline — bounded-git-output invariant |

**`internal/entity`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `registry_test.go` | REWRITE_FOR_RSG | `TestDescriptorsCoverCanonicalFamilies` | descriptor coverage/order/filter/continuation + one-batch read — registry invariants; GTW family set re-authored |

**`internal/fsutil`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `atomic_test.go` | PRESERVE_INVARIANT | `TestReadFileBoundedRejectsOversizedInput` | bounded read rejects oversized input — bound invariant |

**`internal/gates`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `gates_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `gates_tests_gate_test.go` | REWRITE_FOR_RSG | `TestGateTimingWarningIsNonfatalAndBounded` | bounded gate output, executor full-output-on-fail — gate-execution bounds |
| `gates_tests_gate_token_test.go` | REWRITE_FOR_RSG | `TestResolveDefaultsToTheThreeStandardGates` | standard-gate set, token admission, format-scope — gate-token contract under new gate model |
| `test_scope_test.go` | PRESERVE_INVARIANT | `TestResolveTestScopeMapsNestedGoFilesDeterministically` | reverse-transitive dep closure deterministic + fails-closed + docs-exempt — package-graph scope invariants (repo-agnostic mechanism) |

**`internal/gitx`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `correction_test.go` | PRESERVE_INVARIANT | `TestChangedFilesUsesCommittedDiff` | committed-diff changed-files — diff invariant |
| `default_branch_sync_test.go` | PRESERVE_INVARIANT | `TestSynchronizeDefaultBranchWorktreeStrictFastForwardAndIdempotence` | strict fast-forward/idempotent + dirty/wrong-branch/divergence rejection — sync invariants |
| `git_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `git_tests_branch_resolution_test.go` | PRESERVE_INVARIANT | `TestMirrorReadsAllRefsWithoutSwitchingWorktree` | ref reads without worktree switch, worktree fingerprint, missing-branch distinguish — ref-resolution invariants |
| `git_tests_mirror_reconciliation_test.go` | PRESERVE_INVARIANT | `TestReconcileManagedMirrorRejectsSymlink` | symlink/URL-conflict reject, canonical-head refresh, no-external-mutation — mirror invariants |
| `local_revision_test.go` | REWRITE_FOR_RSG | `TestExactReadsPreferRegisteredManagedWorktree` | managed-worktree preference — managed-registry coupling |
| `onboard_test.go` | REWRITE_FOR_RSG | `TestBootstrapEmptyCreatesOnlyDeterministicReadmeOnMain` | deterministic README bootstrap — onboarding shape re-authored |
| `pagination_scope_test.go` | PRESERVE_INVARIANT | `TestVisitDiffStopsAtSemanticLineWithoutReadingTail` | diff/log cursor bounds + revision-scoped cursors — bounded-traversal invariants |
| `repository_prefix_test.go` | PRESERVE_INVARIANT | `TestValidateCommitPrefixMatchesFailsClosedOnSamePrefix` | path-prefix safety — path invariant |

**`internal/gofmtstruct`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `formatter_test.go` | PRESERVE_INVARIANT | `TestFormatSourceUsesVerticalKeyedStructLiterals` | formatter golden semantics |

**`internal/hub`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `correction_test.go` | PRESERVE_INVARIANT | `TestWriteJSONRejectsSymlinkTraversal` | WriteJSON rejects symlink traversal — write-path safety invariant |
| `hub_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `hub_tests_hub_branch_test.go` | PRESERVE_INVARIANT | `TestConcurrentRepositoryWorkersRegainLock` | concurrent lock regain, operation attribution, ensure-preserve/mismatch-reject — remote-repo locking/ensure invariants |
| `hub_tests_read_file_test.go` | PRESERVE_INVARIANT | `TestEnsureCreatesManagedCloneAndMissingBranch` | managed clone/branch ensure, bounded subphases, one-revision snapshot, per-file+aggregate bounds — remote-read invariants |
| `snapshot_test.go` | PRESERVE_INVARIANT | `TestReadSnapshotServesReadsWithoutRefetch` | snapshot serves reads without refetch — snapshot invariance |

**`internal/lockfile`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `lockfile_test.go` | PRESERVE_INVARIANT | `TestLockRejectsConcurrentOwnerAndReacquiresAfterRelease` | TestLockRejectsConcurrentOwnerAndReacquiresAfterRelease + TestReadContentionEvidenceUsesKernelOwner — concurrent ownership/reacquire + kernel-owner evidence |

**`internal/mcp`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `admin_actions_test.go` | DROP_LEGACY_COMPATIBILITY | `TestAdminOnboardActionIsFrozenAndUnregistered` | frozen-admin-domain hidden — GTW admin surface |
| `adr84_mcp_v1_fixture_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestADR84MCPV1ProtectedInputFixture` | protected-input fixture contract — transport boundary proof on new surface |
| `adr84_mcp_v1_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestADR84PublicMCPBoundaryIsSixToolsAndBounded` | public boundary tool-count/envelope/schema — public-surface contract re-proved |
| `agent_cli_submit_test.go` | UNCLEAR | `TestTSK640AgentCLISubmitEndpointsDispatchCanonicalStageActions` | agent-CLI submit endpoints/loopback/worker-session proofs — agent-runtime model undecided |
| `agent_guide_test.go` | REWRITE_FOR_RSG | `TestTSK545AgentGuideIsClosedBoundedAndRoleAware` | closed bounded role-aware guide — guide contract under new roles |
| `agent_local_authority_test.go` | REWRITE_FOR_RSG | `TestCanonicalAgentAwaitUsesLocalAuthorityWhenHubUnavailableAndLocked` | local authority when remote unavailable — authority-fallback invariant |
| `agent_public_mcp_http_e2e_test.go` | UNCLEAR | `TestCanonicalAgentPublicMCPHTTPContractCoversAllActions` | agent public contract — agent model undecided |
| `agent_session_helpers_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `agent_session_test.go` | UNCLEAR | `TestAgentSessionToolsUseRegisteredProjectAndDoNotMutateDurableWorkflow` | agent session tooling — agent model undecided |
| `apps_sdk_contract_test.go` | UNCLEAR | `TestRemovedRunToolsAreNotRegistered` | Apps-SDK tool contract — surface adoption undecided |
| `apps_sdk_support_test.go` | UNCLEAR | `(no Test funcs)` | Apps-SDK support — undecided (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `bounded_collection_contract_test.go` | PRESERVE_INVARIANT | `TestPublicGitRevisionInputsRejectFullObjectIDs` | fingerprint strictness, continuation-in-envelope, bounded contract — output-contract invariants (re-pointed at new corpus) |
| `callback_public_e2e_test.go` | UNCLEAR | `TestCallbackActionsAreRemovedInFavorOfConfigurationHooks` | callback-removal + dynamic-procedure discovery — callback/procedure surface undecided |
| `code_public_e2e_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — e2e helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `code_public_e2e_tests_fixture_read_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — fixtures (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `code_public_e2e_tests_local_search_test.go` | REWRITE_FOR_RSG | `TestPublicCodeSearchAndDiffOverflowE2ELocalSetup` | public code search/diff bounds + envelope E2E — code-action invariants under new corpus |
| `code_public_e2e_tests_search_test.go` | REWRITE_FOR_RSG | `TestPublicCodeActionsE2EPerformanceAndPagination` | public code action E2E + pagination/context bounds |
| `code_read_contract_test.go` | REWRITE_FOR_RSG | `TestCodeActionsAreSessionBoundAndProjectIsNotCallerSelectable` | session-bound code actions + token-budget pagination — code-contract invariants |
| `compact_projections_test.go` | REWRITE_FOR_RSG | `TestControlAndReceiptActionsDoNotAdvertiseDetailProjection` | projection classification coverage + no-durable-leak — projection-policy invariants under new corpus |
| `correction_test.go` | PRESERVE_INVARIANT | `TestToolCallRejectsUnknownTopLevelArgument` | tool-call rejects unknown top-level/envelope fields — transport strictness |
| `debug_actions_test.go` | DROP_LEGACY_COMPATIBILITY | `TestDebugDomainIsAbsentWhenDisabled` | debug-domain action inventory — debug surface |
| `frozen_connector_e2e_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestADR84FrozenConnectorContract` | frozen connector contract — transport-frozen proof on new surface |
| `gateway_status_shared_test.go` | REWRITE_FOR_RSG | `TestRuntimeStatusExposesRuntimeOnlyProjectionWithoutHub` | runtime-only projection without remote — local-authority status invariant |
| `generic_action_behavior_parity_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTypedAndGenericTaskListParity` | typed↔generic parity — parity harness on new action set |
| `generic_agent_actions_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `generic_agent_actions_tests_agent_wait_core_test.go` | UNCLEAR | `TestCanonicalAgentMessageValidationUsesUTF8ByteBound` | agent await/message bounds — agent model undecided |
| `generic_agent_actions_tests_agent_wait_test.go` | UNCLEAR | `TestTSK514AgentInventoryKeepsDisabledCodingAndHidesRetiredWatcher` | agent await/probe/cancellation — agent model undecided |
| `generic_agent_tail_selection_test.go` | UNCLEAR | `TestGenericAgentTailSelectsOnlyUnambiguousDurableAgentSession` | durable-agent tail selection — undecided |
| `generic_generic_authority_tests_test.go` | REWRITE_FOR_RSG | `TestGenericRegisteredActionDiscoveryAndCall` | generic discovery/call + authority reuse + envelope/path contracts — transport-contract invariants |
| `generic_generic_transport_tests_test.go` | REWRITE_FOR_RSG | `TestGenericSessionStartIsDiscoverableAndCreatesPlannerSession` | session-start discovery + compact app-independent transport schemas |
| `generic_message_actions_test.go` | REWRITE_FOR_RSG | `TestTSK589MessageActionsAreClosedAndSessionBound` | closed session-bound message actions — message contract |
| `generic_runtime_logs_test.go` | REWRITE_FOR_RSG | `TestRuntimeLogsIsBoundedReadOnlyGenericAction` | bounded read-only runtime logs + restart boundary receipt |
| `generic_system_await_test.go` | PRESERVE_INVARIANT | `TestSystemAwaitCompletesWithTimingResult` | await completion/cancellation/budget/mutual-exclusion — generic await machinery invariants |
| `generic_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `generic_transport_pagination_test.go` | PRESERVE_INVARIANT | `TestGenericCallEnvelopeDetachesContinuationAndPreservesPayload` | continuation detachment/preservation, sanitization, frozen-path rejection — transport-pagination invariants |
| `guide_binding_test.go` | REWRITE_FOR_RSG | `TestTSK532GuideActionsExposeApplicableSubjectsAndPlannerBinding` | guide subject/provenance fail-closed — guide contract |
| `lifecycle_conflict_test.go` | PRESERVE_INVARIANT | `TestGenericTransportPreservesStructuredLifecycleConflict` | structured lifecycle-conflict transport preservation |
| `managed_project_resolution_test.go` | REWRITE_FOR_RSG | `TestManagedProjectResolutionMCPCapabilitiesAndGitAreDynamic` | dynamic project resolution + fail-closed registry + git-surface exclusion |
| `mcp7_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `mcp7_tests_session_project_test.go` | REWRITE_FOR_RSG | `TestPublicSchemaFiltersActionsByImmutableSessionRole` | immutable-role schema filtering + project-bound session flow + corrupt-code fail-closed |
| `mcp7_tests_session_status_test.go` | REWRITE_FOR_RSG | `TestBootstrapFirstPublicSurfaceIsExact` | bootstrap public surface exactness + token contract + role-rejection + fresh-after-terminate |
| `operator_admin_session_test.go` | UNCLEAR | `TestOperatorAdminSessionRoutesMintAndRevokeThroughDaemonAuthority` | operator admin-session route — operator surface undecided |
| `operator_project_token_test.go` | REWRITE_FOR_RSG | `TestOperatorProjectTokenRouteUsesOwnerPrivateTokenAndExplicitProjectCode` | owner-private token + explicit project code — token-route contract |
| `project_operational_status_test.go` | REWRITE_FOR_RSG | `TestProjectStatusIsSessionBoundCompactOperationalRead` | session-bound compact operational read |
| `runtime_identity_http_test.go` | UNCLEAR | `TestTSK629DurableWorkflowSessionsOverHTTP` | durable role sessions over HTTP + agent targeting — role/agent model undecided |
| `sectional_plan_test.go` | UNCLEAR | `TestRetiredPlanActionsAreAbsentFromCanonicalRegistry` | retired-plan absence + plan-read bounds — plan format undecided |
| `server_http_response_release_test.go` | PRESERVE_INVARIANT | `TestResponseReleaseRunsOnlyAfterResponseFlush` | response-flush-before-deferred + hard-buffer bound — HTTP boundary invariants |
| `server_test.go` | PRESERVE_INVARIANT | `TestToolsListAndToolResultsUseObjects` | tools objects + authority boundary + nonloopback rejection — server invariants |
| `session_authority_test.go` | PRESERVE_INVARIANT | `TestGenericCallValidatesCompiledInputBeforeActionExecution` | compiled-input validation + per-action durable-session auth — auth-boundary invariants |
| `session_bound_schema_test.go` | PRESERVE_INVARIANT | `TestSessionBoundActionSchemasDoNotExposeProjectID` | session-bound schemas hide project — schema-boundary invariant |
| `session_test.go` | REWRITE_FOR_RSG | `TestSessionInputSchemaAdvertisesCanonicalActionsAndIDs` | session lifecycle/bootstrap/list — session contract under new role model |
| `session_test_helpers_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `task_authoring_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `task_execution_test.go` | REWRITE_FOR_RSG | `TestTaskExecutionSchemasAreTaskIdentityOnly` | task-execution schema task-identity-only — contract |
| `task_list_contract_test.go` | REWRITE_FOR_RSG | `TestTaskListSchemaUsesCanonicalBoundedSurface` | bounded task-list surface contract |
| `tsk384_rule_actions_test.go` | REWRITE_FOR_RSG | `TestTSK384RuleActionSurfaceIsCanonicalAndComplete` | rule action surface completeness + closed schemas |
| `tsk409_adr_schema_test.go` | REWRITE_FOR_RSG | `TestTSK409ADRPublicSchemasAreClosedAndTransportNeutral` | ADR public schemas closed/transport-neutral |
| `tsk409_rev7_adr_contract_test.go` | REWRITE_FOR_RSG | `TestTSK409Rev7ADRReadOmitsAbsentLegacySummary` | ADR read/list/archive/revision/pagination contract |
| `tsk433_journal_actions_test.go` | REWRITE_FOR_RSG | `TestTSK433JournalActionSurfaceIsCanonicalAndSessionBound` | journal surface session-bound + transport round-trip |
| `tsk511_relation_contract_test.go` | REWRITE_FOR_RSG | `TestTSK511RelationActionContract` | relation contract + transport + pagination |
| `tsk521_task_execution_schema_test.go` | REWRITE_FOR_RSG | `TestTSK521TaskExecutionSchemasAreClosedAndPubliclyBounded` | execution schema closed/bounded |
| `tsk531_task_contract_test.go` | REWRITE_FOR_RSG | `TestTSK531CanonicalTaskSurfaceAndLegacyEvidenceContract` | task surface + history projection + legacy-evidence contract |
| `tsk540_catalogue_contract_test.go` | REWRITE_FOR_RSG | `TestTSK540CatalogueSchemasUseOuterContinuationAndCompactMilestones` | catalogue outer-continuation + milestone pagination |
| `tsk563_generic_agent_runtime_test.go` | UNCLEAR | `TestTSK629PlannerObservesAndLeadControlsLogicalAgentThroughDurableSession` | lead-control/agent-session routing — agent model undecided |
| `tsk564_task_update_parity_test.go` | REWRITE_FOR_RSG | `TestTSK564TaskUpdateSchemaAndHandlerUseKey` | task-update schema/handler parity |
| `tsk567_task_read_test.go` | REWRITE_FOR_RSG | `TestTSK567TaskReadAgentContractIsBoundedAndReadOnly` | task-read bounded read-only contract |
| `tsk574_worker_binding_test.go` | UNCLEAR | `TestTSK574WorkerRestartReresolvesExistingAttachment` | worker binding/rebind semantics — role/agent model undecided |
| `tsk577_status_policy_test.go` | REWRITE_FOR_RSG | `TestTSK577ADRStatusSchemasAndRuntimeUseDescriptorPolicy` | ADR status descriptor-policy contract |
| `tsk578_role_registry_test.go` | REWRITE_FOR_RSG | `TestTSK578RoleRegistryFeedsPublicSchemasAndProjections` | role registry → schemas + durable-session resolution — under new role model |
| `tsk580_usage_boundary_test.go` | UNCLEAR | `TestTSK580TelemetryFailureDoesNotChangeAuthenticatedActionResult` | telemetry boundary — telemetry adoption undecided |
| `tsk585_task_contract_regression_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK585TaskCompleteMCPSchema` | task-complete MCP contract regression — reprove on new surface |
| `tsk590_lead_binding_test.go` | UNCLEAR | `TestTSK590LeadBindingBindRebindAndGatewayRestart` | lead binding + role isolation — role model undecided |
| `tsk593_lead_track_orchestration_test.go` | UNCLEAR | `TestTSK593LeadRunsEligibleTrackMembersSequentiallyAndSubmitsDerivedReady` | lead track orchestration + durable-MSG resume — role model undecided |
| `tsk595_action_inventory_test.go` | REWRITE_FOR_RSG | `TestTSK595FrozenActionInventory` | frozen action inventory + shared-definitions/schema cost — corpus invariants (Lead-noted current contract proofs) |
| `tsk598_action_contract_test.go` | REWRITE_FOR_RSG | `TestTSK598FrozenActionContractsAndRegisteredHandlers` | frozen action contracts + handler registry + deterministic catalog load — corpus invariants |
| `tsk599_contract_cleanup_test.go` | REWRITE_FOR_RSG | `TestTSK670SchemaCostFitsResetContractBudget` | schema-cost budget + no-superseded-registries + contract-projection coverage |
| `tsk600_multi_agent_runtime_test.go` | UNCLEAR | `TestTSK629RuntimeKeyFailsClosedDespiteAmbiguousBindings` | runtime-key ambiguity + routing separation — agent model undecided |
| `tsk608_operation_contract_test.go` | REWRITE_FOR_RSG | `TestTSK608OperationActionsAreBoundedReadOnlyAndDistinctFromAgentAwait` | operation actions bounded read-only contract |
| `tsk625_task_execution_contract_test.go` | REWRITE_FOR_RSG | `TestTSK625TaskStatusCarriesOptionalBlockedReason` | blocked-status + block/resume closed contracts |
| `tsk629_session_auth_regression_test.go` | REWRITE_FOR_RSG | `TestTSK629PublicCallHardCutsRuntimeSessionFallback` | durable-session auth hard-cut + role matrix + public-boundary inventory — under new roles |
| `tsk630_debug_tail_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK630DebugTailUsesDirectAgentRefAndBoundedRecoverySelector` | debug-tail actions — debug surface |
| `tsk631_task_execution_refresh_contract_test.go` | REWRITE_FOR_RSG | `TestTSK631TaskRefreshHasClosedLeadContract` | refresh closed lead contract |
| `tsk636_permissive_authorization_test.go` | REWRITE_FOR_RSG | `TestTSK636AuthenticatedWorkflowRolesShareSchemaAndActionAuthorization` | authenticated-role schema/action authorization |
| `tsk644_code_search_grammar_test.go` | REWRITE_FOR_RSG | `TestTSK644CodeSearchSchemaExposesLiteralAlternativesAndCaseMode` | code-search literal/case grammar + E2E |
| `tsk645_code_diff_head_test.go` | REWRITE_FOR_RSG | `TestTSK645CodeDiffSchemaExposesAuthoritativeHead` | code-diff authoritative head + fail-closed E2E |
| `tsk650_bootstrap_contract_test.go` | REWRITE_FOR_RSG | `TestTSK650ProjectSessionDiscoveryIsScopedAndTokenNeverProjects` | bootstrap discovery scoped, token never projects |
| `tsk658_guide_workflow_test.go` | REWRITE_FOR_RSG | `TestTSK658TaskGuideSingleSubmitWorkflow` | guide single-submit workflow |
| `tsk660_track_contract_test.go` | REWRITE_FOR_RSG | `TestTSK660TrackAndMilestoneActionContracts` | track+milestone action contracts |
| `tsk663_milestone_plan_test.go` | REWRITE_FOR_RSG | `TestTSK663MilestonePlanUsesCompiledActionContract` | milestone-plan compiled-contract proof |
| `tsk669_compact_output_contract_test.go` | REWRITE_FOR_RSG | `TestTSK669NormalOutputsRejectEchoesDefaultsAndTelemetry` | normal-output rejections + compact token cost |
| `tsk670_task_execution_reset_contract_test.go` | REWRITE_FOR_RSG | `TestTSK670TaskResetContractAndPlannerAuthority` | reset contract + planner authority |
| `tsk693_procedure_e2e_test.go` | UNCLEAR | `TestTSK693E2ETrackAcceptCrossProject` | procedure/e2e track-accept — procedure surface + GTW procedure undecided |
| `workflow_policy_status_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `public_code_performance_test.go *(build-tag liveperformance)*` | UNCLEAR | `TestPublicCodeLatencyPerformanceProfile` | liveperformance-tagged latency profile — timing-only, no recorded baseline |

**`internal/model`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `agent_test.go` | REWRITE_FOR_RSG | `TestAgentValidationIsClosedAndPortable` | agent validation closed/portable — under agent-model decision |
| `correction_test.go` | PRESERVE_INVARIANT | `TestADRIdentifierRejectsPathTraversal` | ADR identifier path-traversal + relative-path/git/backslash rejection — path-safety invariant |
| `entity_models_test.go` | REWRITE_FOR_RSG | `TestCanonicalEntityIDsAreProjectScopedAndBounded` | project-scoped bounded entity IDs + rule/message bounds — ID format re-authored |
| `guide_applicability_test.go` | REWRITE_FOR_RSG | `TestGuideApplicabilityMatrixIsCompleteAndExplained` | applicability matrix completeness + closed deterministic projection |
| `milestone_test.go` | REWRITE_FOR_RSG | `TestMilestoneLifecycleAndMembershipValidation` | milestone lifecycle/membership + bounded evidence references |
| `operation_identifiers_test.go` | REWRITE_FOR_RSG | `TestOperationIdentifiersUseCanonicalProjectScopedFormat` | operation ID canonical project-scoped format — format re-authored |
| `operator_journal_schema_oracle_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — oracle helper (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `operator_journal_schema_test.go` | UNCLEAR | `TestOperatorJournalStaticSchemaEvaluatesCompleteParityFixtures` | operator-journal schema parity fixtures — document-format adoption undecided |
| `operator_journal_test.go` | UNCLEAR | `TestOperatorJournalKindParityAndStrictValidation` | operator-journal validation/IDs/correction semantics — document-format adoption undecided |
| `project_callbacks_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `project_configuration_test.go` | REWRITE_FOR_RSG | `TestProjectConfigurationV3DefaultsValidate` | config v3 defaults + procedure schema/hook compatibility — config-contract invariants |
| `project_identifiers_test.go` | REWRITE_FOR_RSG | `TestProjectIdentifiersValidationAndCompactIDs` | identifier validation/compact-IDs/integer-parity — ID semantics |
| `sectional_plan_test.go` | REWRITE_FOR_RSG | `TestPlanSchemaV2ValidationRejectsLegacyAndDuplicateSections` | plan v2 schema strictness + legacy/duplicate rejection — plan format adoption conditional |
| `semantic_task_test.go` | REWRITE_FOR_RSG | `TestSemanticTaskAllowsOmittedBaseRevision` | omitted/explicit base-revision semantics |
| `task_authoring_test.go` | REWRITE_FOR_RSG | `TestTaskAuthoringValidationAndReadySeal` | authoring validation/ready-seal + scope/execution hashing |
| `task_execution_state_test.go` | REWRITE_FOR_RSG | `TestTSK521TaskExecutionStateValidation` | execution-state conditional validation |
| `task_lifecycle_test.go` | REWRITE_FOR_RSG | `TestTaskLifecycleStatesValidateTheirConditionalFields` | lifecycle conditional fields + strict commit-SHA |
| `task_priority_test.go` | REWRITE_FOR_RSG | `TestTaskPriorityEnumAndCanonicalRank` | priority enum/rank + executable-boundary requiredness |
| `task_scope_test.go` | REWRITE_FOR_RSG | `TestTaskScopeNormalizesAndBoundsFields` | scope normalization/bounds + execution defaults |
| `task_status_symbol_test.go` | REWRITE_FOR_RSG | `TestTaskStatusSymbolUsesADR107Vocabulary` | status-symbol vocabulary (ADR107) |
| `task_type_test.go` | REWRITE_FOR_RSG | `TestTaskTypeDefaultsAndValidatesClosedEnum` | closed enum defaults + legacy revision-hash preservation |
| `tsk384_rule_validation_test.go` | REWRITE_FOR_RSG | `TestTSK384RuleNameSyntaxIsClosed` | rule name/form closure + typed values |
| `tsk409_rev7_adr_validation_test.go` | REWRITE_FOR_RSG | `TestTSK409Rev7ADRSummaryValidationIsCurrentStrictAndHistoricalTolerant` | ADR summary strict-current/historical-tolerant + rune bounds |
| `tsk521_task_terminal_validation_test.go` | REWRITE_FOR_RSG | `TestTSK521TerminalTaskReadySealValidation` | terminal ready-seal validation |
| `tsk531_task_contract_test.go` | REWRITE_FOR_RSG | `TestTSK531TaskSummaryAndHistoricalHashContract` | task summary/history hash contract + ADR relation rules |
| `workflow_policy_test.go` | REWRITE_FOR_RSG | `TestProjectWorkflowPolicyHasNoGenericGateSelection` | no generic gate selection — policy invariant |

**`internal/pagination`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `pagination_test.go` | PRESERVE_INVARIANT | `TestPageUsesOpaqueDeterministicContinuation` | opaque deterministic continuation + stale/ambiguous/out-of-scope rejection + bounds — cursor invariants |

**`internal/publicprojection`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `projection_test.go` | REWRITE_FOR_RSG | `TestProjectCompactsGitFingerprintsAndOmitsNonGitDigests` | fingerprint compaction, ambiguity rejection, git-action-only text, non-git digest preservation — projection invariants; proofs bind GTW action IDs → re-point |

**`internal/releaseartifacts`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `releaseartifacts_test.go` | REWRITE_FOR_RSG | `TestBinarySourceRevisionRequiresExactEmbeddedSHA` | embedded-SHA revision, bounded probe, complete artifact replace/snapshot/restore — artifact-integrity invariants; release-pipeline adoption conditional |

**`internal/runtime_log`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `store_test.go` | PRESERVE_INVARIANT | `TestStoreAppendReadFiltersAndRedacts` | append/read filters+redaction, rotate+malformed tolerance, bounded strict JSON — event-log invariants |

**`internal/service`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `admin_onboarding_test.go` | UNCLEAR | `TestAdminRepositoryValidationRequiresCanonicalAllowlistedGitHubIdentity` | canonical-allowlisted remote identity + worker rollback — admin surface undecided |
| `adr_create_async_test.go` | REWRITE_FOR_RSG | `TestADRCreateAsyncIsBoundedAndIdempotent` | bounded idempotent async create — durable-async invariant |
| `agent_interrupt_interrupt_test.go` | UNCLEAR | `TestAgentInterruptCurrentAgentDoesNotRequireExecutionAttempt` | interrupt without execution attempt — agent model undecided |
| `agent_ipc_async_test.go` | UNCLEAR | `TestAgentIPCMutationsReturnBoundedReceipts` | IPC admission/idempotent-retry/turn semantics — agent model undecided |
| `agent_prompt_test.go` | UNCLEAR | `TestCompactAgentPromptResultSuccessOmitsExecutionReceipt` | bounded prompt projections — agent model undecided |
| `agent_register_test.go` | UNCLEAR | `TestAgentRegisterCreatesLocalAgentWithoutHubState` | register without remote writes + duplicate reject — agent registry undecided |
| `agent_routing_test.go` | UNCLEAR | `TestResolveAgentUsesExplicitProjectBinding` | explicit project-binding resolution — agent model undecided |
| `agent_transcript_test.go` | UNCLEAR | `TestAgentTailUsesExplicitSessionWithoutDurableAgentLookup` | tail viewport/continuation/session scoping — agent transcript undecided |
| `callback_registry_test.go` | DROP_LEGACY_COMPATIBILITY | `TestProjectConfigurationReplacesCallbackSectionWithHooks` | proves callback section replaced by hooks — GTW cutover proof |
| `integration_hooks_test.go` | REWRITE_FOR_RSG | `TestProjectConfigurationHasNoInlineIntegrationCommandAuthority` | no inline integration-command authority — hook-safety invariant |
| `journal_migration_test.go` | DROP_LEGACY_COMPATIBILITY | `TestJournalMigrateCuratedReexpressesLegacyEvents` | legacy journal re-expression/inbox drain — GTW migration content |
| `journal_stream_test.go` | REWRITE_FOR_RSG | `TestJournalContractExposesClosedStreams` | closed streams, writer authority, contract-before-commit, durable publish — journal invariants |
| `liveness_progress_test.go` | REWRITE_FOR_RSG | `TestProjectStatusAggregatesProgressWithoutSessionIdentity` | progress aggregation + retired-family/corruption tolerance |
| `local_code_inspection_latency_test.go` | REWRITE_FOR_RSG | `TestCodeSearchCleanMainResolutionFunctional` | clean-main resolution correctness — inspection invariant |
| `local_code_inspection_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `local_code_inspection_test_doubles_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — fixture doubles (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `local_code_inspection_tests_local_fixture_test.go` | PRESERVE_INVARIANT | `TestLocalCodeInspectionUsesCleanAncestorAndBoundedCommittedObjects` | clean-ancestor + bounded committed-object reads — inspection invariants |
| `local_code_inspection_tests_local_search_test.go` | PRESERVE_INVARIANT | `TestCodeWorktreeIgnoresHistoricalHotfixLane` | exact scan-position continuation + bounded context + selector distinctness |
| `local_code_inspection_tests_ranges_test.go` | PRESERVE_INVARIANT | `TestLocalCodeReadSupportsExactBoundedRangesAndContinuation` | exact bounded ranges + compact continuation |
| `local_code_inspection_tests_tsk582_test.go` | PRESERVE_INVARIANT | `TestTSK582CodeSearchScanBudgetReturnsPartialPageAndContinuation` | scan-budget partial page + deterministic continuation + request bounds |
| `local_code_inspection_tests_worktree_binding_test.go` | REWRITE_FOR_RSG | `TestCodeWorktreeIgnoresHistoricalHotfixIdentityRecord` | worktree discovery/selector binding — worktree-model coupling noted |
| `local_code_inspection_tests_worktree_ordering_test.go` | REWRITE_FOR_RSG | `TestSortCodeWorktreeCandidatesUsesKindThenNewestCreationAndCanonicalIDDescending` | candidate ordering/packing/pagination + historical-lane skips |
| `local_code_inspection_tests_worktree_tree_test.go` | REWRITE_FOR_RSG | `TestLocalCodeReadFileHashIsWholeFileAndStableAcrossPages` | whole-file hash stability + committed-vs-live hash + canonical-refresh |
| `managed_agent_identity_test.go` | UNCLEAR | `TestTSK620ManagedAgentMigrationPreservesWorkerSessionAndRestarts` | agent identity preserve/collision — agent model undecided |
| `message_lifecycle_test.go` | REWRITE_FOR_RSG | `TestTSK589PLAWMessageLifecycleRoutesIsolationBoundsAndPaging` | role-routed message isolation/bounds/atomic cancel — message invariants (PLAW routing re-authored) |
| `milestone_lifecycle_test.go` | REWRITE_FOR_RSG | `TestMilestoneLifecycleCASMembershipCompletionAndArchive` | milestone CAS/membership/completion/archive + non-done fail-closed |
| `milestone_plan_test.go` | REWRITE_FOR_RSG | `TestTSK663MilestonePlanRendersCurrentTracksAndUngroupedTasks` | plan rendering + output bound — milestone-plan invariant |
| `not_found_test.go` | PRESERVE_INVARIANT | `TestIsNotFoundUsesTypedWrappedErrorsOnly` | typed wrapped-error only — error-semantics invariant |
| `operation_recovery_test.go` | PRESERVE_INVARIANT | `TestRecoverRunningDurableMutationForStartup` | startup recovery, bounded ctx, no-replay, per-kind timeout, bounded-captured-state compat — durable-mutation recovery invariants |
| `operator_journal_evidence_seed_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — seed helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `operator_token_test.go` | REWRITE_FOR_RSG | `TestOperatorTokenIsStableOwnerPrivateAndValidated` | stable owner-private validated token — token invariant |
| `procedure_execution_test.go` | REWRITE_FOR_RSG | `TestProcedureExecutionUsesBoundedStructuredEnvelope` | bounded structured envelope, non-Go script, invalid-output/escaping-path reject, hook operation allocation — procedure-sandbox invariants |
| `project_configuration_async_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `project_configuration_async_tests_project_config_core_test.go` | REWRITE_FOR_RSG | `TestProjectConfigurationMutationsDoNotAllocateOperations` | config mutations allocate no operations — mutation semantics |
| `project_configuration_async_tests_project_config_test.go` | REWRITE_FOR_RSG | `TestProjectConfigurationReadUsesSharedWhenHubUnavailable` | shared-read fallback, CAS+outbox mutation, planner+reason gate, canonical payload — config-authority invariants |
| `project_configuration_hub_migration_test.go` | DROP_LEGACY_COMPATIBILITY | `TestProjectConfigurationHubMigrationFailsClosedOnRetiredGateCommands` | GTW hub-config migration content |
| `project_configuration_test.go` | REWRITE_FOR_RSG | `TestProjectConfigurationProcedureAndHookManagement` | procedure/hook management + v3 defaults |
| `project_identifiers_test.go` | REWRITE_FOR_RSG | `TestProjectIdentifiersReadRequiresExistingStrictRecord` | strict-record read, adopt, duplicate-code single-winner — identifier invariants |
| `project_operational_status_shared_test.go` | REWRITE_FOR_RSG | `TestProjectOperationalStatusUsesLocalSharedStateWhenHubUnavailable` | local Shared authority when remote unavailable — authority invariant |
| `project_resolver_test.go` | REWRITE_FOR_RSG | `TestProjectResolutionAbsentRegistryPreservesStaticBehaviorAndFreshMaps` | registry-preserving/fail-closed resolution — resolver invariants |
| `project_retirement_test.go` | REWRITE_FOR_RSG | `TestDebugProjectRetirementTransitionExcludesStaleProjectsAndPreservesGateway` | retirement transition excludes stale, preserves gateway |
| `project_token_test.go` | REWRITE_FOR_RSG | `TestProjectTokenStaticGrantIsStableAndIdentityBound` | stable identity-bound token + ambiguous-root fail-closed |
| `project_update_test.go` | REWRITE_FOR_RSG | `TestProjectUpdateBootstrapCorrectionPreservesAuthoritiesAndCounters` | correction preserves authorities/counters + virgin-conflict/dup reject — update invariants |
| `sectional_plan_test.go` | UNCLEAR | `TestPlanCutoverPreservesLegacySemanticsAndIsOneTime` | plan cutover/retired mutations — plan format undecided |
| `service_gates_test.go` | REWRITE_FOR_RSG | `TestResolveProjectGatesUsesProjectPolicyAndLegacyDefault` | project-policy gates, server-owned results, resolved-scope receipts — gate-resolution invariants |
| `service_startup_test.go` | REWRITE_FOR_RSG | `TestStartupServiceDefersDurableRecoveryWorkers` | deferred durable-recovery workers — startup invariant |
| `service_test_helpers_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `service_tests_helpers_tests_session_fixture_test.go` | DROP_LEGACY_COMPATIBILITY | `TestValidateConfiguredProjectRecordsRejectsMissingDurableRecord` | fixture-validation helper — rehome with fixtures |
| `service_tests_helpers_tests_task_project_test.go` | REWRITE_FOR_RSG | `TestTaskCreateRequiresDurableProjectRecordWithoutGitLookup` | task-create requires durable project record — authority invariant |
| `session_test.go` | REWRITE_FOR_RSG | `TestServiceSessionLifecycleUsesRegisteredProject` | session lifecycle uses registered project |
| `shared_outbox_worker_test.go` | REWRITE_FOR_RSG | `TestSharedOutboxRetryDelayIsBounded` | bounded retry, terminal no-ops, convergence, publish — outbox invariants |
| `shared_restore_test.go` | PRESERVE_INVARIANT | `TestPortableHubRestoreHydratesTrackRelationsAndSequences` | restore hydrates relations/sequences + converges — restore invariants |
| `state_check_snapshot_test.go` | REWRITE_FOR_RSG | `TestStateCheckWithoutDurabilityUsesLocalConfigurationWithoutHub` | local-only state check when remote unavailable/locked — local-authority invariant |
| `task_authoring_authoring_helpers_test.go` | REWRITE_FOR_RSG | `TestTaskAuthoringFindSkipsEarlierLegacyProject` | legacy-project skip — authority invariant |
| `task_authoring_authoring_lifecycle_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `task_authoring_ready_async_test.go` | REWRITE_FOR_RSG | `TestTaskAuthoringReadyAsyncIsBoundedAndIdempotent` | bounded idempotent ready — durable-async invariant |
| `task_authoring_shared_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `task_authoring_shared_tests_shared_task_test.go` | REWRITE_FOR_RSG | `TestSharedQueriesScopeBeforeGlobalPageLimit` | scope-before-page-limit, dedupe, degraded-remote commit, ready-requires-integration — shared-authority invariants |
| `task_authoring_shared_tests_task_shared_core_test.go` | REWRITE_FOR_RSG | `TestFreshSharedBaselineAllowsAuthoringWithoutBootstrapMarker` | fresh baseline authoring without bootstrap marker |
| `task_authoring_shared_tests_task_shared_test.go` | REWRITE_FOR_RSG | `TestTaskAuthoringAsyncMutationsCommitSharedBeforeHubSync` | commit-before-sync + shared-read authority — authority invariants |
| `task_authoring_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `task_authoring_update_async_test.go` | REWRITE_FOR_RSG | `TestTaskAuthoringUpdateAsyncIsBoundedIdempotentAndRestartReadable` | bounded idempotent restart-readable update |
| `task_complete_canonical_journal_test.go` | REWRITE_FOR_RSG | `TestTaskCompleteAcceptsCanonicalPlannerNotesReview` | canonical journal-review acceptance — completion-evidence invariant |
| `task_create_async_test.go` | REWRITE_FOR_RSG | `TestTaskAuthoringCreateAsyncIsDurableAndIdempotent` | durable idempotent create |
| `task_execution_bootstrap_reconcile_test.go` | REWRITE_FOR_RSG | `TestTSK678ExactPlannerAndLeadJournalEvidence` | exact journal-evidence + source/candidate/base proof + replay-own-evidence — reconcile invariants |
| `task_execution_integrate_historical_fingerprint_test.go` | REWRITE_FOR_RSG | `TestTaskExecutionHistoricalCommitFingerprintResolution` | historical commit fingerprint resolution |
| `task_execution_integration_recovery_test.go` | PRESERVE_INVARIANT | `TestTSK521PostMainRecoveryReadsIntegratingState` | post-main recovery reads, idempotent retry, partial/mismatch fail-closed — integration-recovery invariants |
| `task_revision_legacy_evidence_test.go` | UNCLEAR | `TestTSK531LegacyEvidenceHistoricalWireHashAndStrictFields` | legacy wire-hash/strict-fields/ownership-parent reject — legacy-decode adoption undecided |
| `task_supersede_async_test.go` | REWRITE_FOR_RSG | `TestTaskSupersedeAsyncIsBoundedAndIdempotent` | bounded idempotent supersede |
| `test_gate_receipt_scope_test.go` | PRESERVE_INVARIANT | `TestScopedReceiptMatrixUsesExactServiceScopeIdentity` | scope-identity receipt + no cross-reuse + legacy/failed fail-closed — receipt invariants |
| `test_gate_receipt_test.go` | PRESERVE_INVARIANT | `TestCanonicalTestGateReceiptReusesIdenticalTree` | tree-identity reuse, dirty/contract invalidation, prospective-tree convergence — receipt-identity invariants |
| `track_dispatch_history_test.go` | REWRITE_FOR_RSG | `TestTSK660TrackRemovalIgnoresGlobalExecutionWithoutTrackHistory` | dispatch-history gating on removal — track invariant |
| `track_lifecycle_test.go` | REWRITE_FOR_RSG | `TestTSK660MilestoneUnorderedSetAndTrackLifecycle` | track lifecycle + canonical remote publish |
| `tsk384_rule_lifecycle_test.go` | REWRITE_FOR_RSG | `TestTSK384RuleCreateProducesProposedOnly` | rule lifecycle/name-uniqueness/non-reuse/digest-ack — rule invariants |
| `tsk409_rev7_adr_lifecycle_test.go` | REWRITE_FOR_RSG | `TestTSK409Rev7ADRStatusOnlyUpdateAndArchiveKeepContentRevision` | ADR revision semantics + degraded-remote transitions + corrupt fail-closed |
| `tsk480_task_priority_test.go` | REWRITE_FOR_RSG | `TestTSK480TaskDispatchRequiresCanonicalPriority` | canonical priority required at dispatch |
| `tsk511_relation_authority_test.go` | REWRITE_FOR_RSG | `TestTSK511RelationCreateClosedKindsAndIdempotence` | relation closed-kinds/idempotence/pagination/historical-scalar isolation |
| `tsk521_task_execution_test.go` | REWRITE_FOR_RSG | `TestTaskExecutionPublicOutputRejectsAmbiguousHeadFingerprint` | ambiguous-head reject + one-frozen-lane + concurrent-dispatch convergence |
| `tsk531_task_lifecycle_authority_test.go` | REWRITE_FOR_RSG | `TestTSK531TaskArchiveSurvivesDegradedHub` | archive/history/list authority + restart survival + orchestration separation |
| `tsk531_task_lifecycle_test.go` | REWRITE_FOR_RSG | `TestTSK531SharedTaskLifecycleContract` | shared lifecycle contract + ready-archive seal semantics |
| `tsk531_task_sequence_test.go` | REWRITE_FOR_RSG | `TestTSK531TaskCreateUsesReconciledSequence` | reconciled-sequence allocation |
| `tsk552_binding_refresh_test.go` | REWRITE_FOR_RSG | `TestTSK552BindingWriteRestoresBytesWhenRefreshFails` | bytes-restore on failed refresh + live binding refresh — binding invariant |
| `tsk552_bootstrap_test.go` | REWRITE_FOR_RSG | `TestTSK552ProjectOnboardDerivesCwdIdentityAndIsIdempotent` | onboard derivation/idempotence/conflict + agent isolation — bootstrap invariants (role subpart re-authored) |
| `tsk585_durable_clock_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — durable-clock helper (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `tsk585_task_execution_regression_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK622DispatchUsesWorkerActionableOwnership` | dispatch-ownership/rework-review/end-to-end execution contract — reprove on new surface |
| `tsk585_task_lifecycle_regression_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK585TaskCompleteIntegrated` | complete integrated/noncode/historical + authority + atomic rollback — reprove on new surface |
| `tsk600_agent_runtime_test.go` | UNCLEAR | `TestTSK600TwoEnabledAgentsBindLeadAndWorkerRuntimes` | runtime binding/collision/enable-disable — agent model undecided |
| `tsk603_submit_origin_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK603SubmitPublishesExactOriginArtifact` | origin-artifact publish/divergence/reconcile contract — reprove on new surface |
| `tsk604_task_verification_environment_test.go` | REWRITE_FOR_RSG | `TestTSK604TaskCompleteAfterRestartIgnoresEnvironmentDrift` | verification immune to env drift + gate-profile independence — verification invariants |
| `tsk608_legacy_receipt_adoption_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK608AdoptsLegacyDurableMutationReceipt` | legacy durable-receipt adoption — GTW receipt compat |
| `tsk608_operation_test.go` | REWRITE_FOR_RSG | `TestTSK608OperationReadAwaitTimeoutAndOutcomeUnknown` | operation read/await/outcome-unknown + project isolation |
| `tsk623_agent_prompt_migration_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK623UpgradedLocalSchemaSupportsNormalAgentPromptAdmission` | upgraded-schema prompt admission — GTW schema-upgrade proof |
| `tsk625_task_execution_regression_test.go` | REWRITE_FOR_RSG | `TestTSK625BlockResumePreservesDurableWorkerLane` | block/resume lane/evidence/fail-closed semantics |
| `tsk631_task_execution_refresh_regression_test.go` | REWRITE_FOR_RSG | `TestTSK631RefreshesBlockedPreworkAndPreservesIdentity` | refresh identity/chain/corrupt-reject/unsafe reject |
| `tsk644_code_search_query_test.go` | PRESERVE_INVARIANT | `TestTSK644CodeSearchLiteralAlternativesMatchInOnePass` | literal-alternatives/escape/case grammar + fail-closed + pagination — query invariants |
| `tsk645_code_diff_head_test.go` | REWRITE_FOR_RSG | `TestTSK645CodeDiffAuthoritativeHead` | authoritative-head diff + bounded pagination |
| `tsk646_test_lane_test.go` | REWRITE_FOR_RSG | `TestTSK646TaskVerificationUsesOrderedProjectProcedureChecks` | ordered procedure checks — verification-lane invariant |
| `tsk649_bootstrap_test.go` | REWRITE_FOR_RSG | `TestTSK649ProjectOnboardEstablishesCompleteSharedBootstrap` | complete bootstrap + partial repair w/o replacement |
| `tsk652_onboarding_token_test.go` | REWRITE_FOR_RSG | `TestTSK652ProjectOnboardReturnsStablePlannerBootstrapToken` | stable planner bootstrap token |
| `tsk653_state_check_test.go` | REWRITE_FOR_RSG | `TestTSK653StateCheckAcceptsManagedProjectWithoutLegacyRelay` | managed-project acceptance + relay-requirement separation |
| `tsk657_submit_transport_test.go` | REWRITE_FOR_RSG | `TestTSK657SubmitAdmissionReconcilesStillExecutingAndCompletedRepeat` | submit-admission reconciliation/retry-only-after-proof/scoping — admission invariants |
| `tsk661_shared_revision_recovery_test.go` | REWRITE_FOR_RSG | `TestTSK661CurrentRevisionReadFallbackAndFrozenAdmission` | current-revision fallback + frozen admission |
| `tsk664_gate20_hub_inventory_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK664Gate20HubFamiliesHaveExplicitDisposition` | remote-family/sequence/revision disposition inventory — re-prove on RSG schema |
| `tsk664_gate20_local_inventory_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK664Gate20LocalExecutionFamiliesAreClassified` | local-execution family classification — re-prove on RSG schema |
| `tsk668_track_reconciliation_test.go` | REWRITE_FOR_RSG | `TestTSK668LegacyTrackAcceptOutboxReconcilesFromSharedHistoryAndDrains` | outbox reconcile from shared history + near-miss/non-regress — reconciliation invariants |
| `tsk670_task_execution_reset_test.go` | REWRITE_FOR_RSG | `TestTSK670TaskExecutionResetThenUpdateAndRedispatch` | reset/evidence/unsafe fail-closed + persistence failure — reset invariants |
| `tsk673_track_reset_projection_test.go` | REWRITE_FOR_RSG | `TestTSK673ResetTaskProjectsPlannedInTrackAndMilestone` | reset projects planned + unproven-abandoned exclusion |
| `tsk680_verification_retry_test.go` | REWRITE_FOR_RSG | `TestTSK680FailedVerificationAdmitsFreshAttempt` | fresh-attempt/in-flight replay/bounded attempts/drift fail-closed — verification invariants |
| `tsk682_retirement_snapshot_test.go` | REWRITE_FOR_RSG | `TestTSK682RetirementPublishCannotBlockOnPinnedSnapshot` | retirement publish non-blocking on pinned snapshot |
| `tsk683_pre_execution_reconcile_test.go` | REWRITE_FOR_RSG | `TestTSK660PlannerAuthorizationContentIsExact` | exact content/gate-predicate/lifecycle/receipt validation — reconcile invariants |
| `tsk688_pre_execution_reconcile_test.go` | REWRITE_FOR_RSG | `TestTSK688AllowlistIsExact` | exact allowlist/authorization/lifecycle/near-miss/receipt-set rejections |
| `tsk690_session_attach_test.go` | REWRITE_FOR_RSG | `TestTSK690SessionAttachCreatesAndReusesManagedRoleSession` | attach create/reuse, role-mismatch/conflict fail-closed — session-attach invariants under new roles |
| `tsk692_hub_adopt_test.go` | REWRITE_FOR_RSG | `TestTSK692ProjectOnboardAdoptsHubKnownProjectFromEmptyLocal` | empty-local adopt+restore + identity-conflict fail-closed — restore invariant |
| `tsk695_e2e_output_test.go` | UNCLEAR | `TestTSK695E2EOutputForeignTrackKeyCompletes` | foreign-project key output + EntityRef fail-closed — output-contract; e2e procedure adoption undecided |
| `tsk696_milestone_track_terminality_test.go` | REWRITE_FOR_RSG | `TestTSK696MilestoneCompleteTreatsStoredAcceptedAsTerminal` | stored-acceptance terminality vs projected stale — terminality invariant |
| `tsk697_accepted_track_fallback_test.go` | REWRITE_FOR_RSG | `TestTSK697IntegratedCompleteAcceptedTrackFallback` | accepted-track fallback + all fail-closed legs — completion-proof invariants |
| `tsk698_zero_criteria_track_test.go` | REWRITE_FOR_RSG | `TestTSK698ZeroCriteriaRequiresTrackUnderCurrentVerification` | zero-criteria track requirement + criteria path unchanged |
| `verify_test.go` | REWRITE_FOR_RSG | `TestVerifySingleFlightReusesCompletedReceipt` | single-flight receipt reuse + fingerprint-new-run + gate-identity op |
| `work_progress_test.go` | REWRITE_FOR_RSG | `TestWorkProgressAdvancesPostGateBaselineAndNextCallUsesDelta` | delta advancement, failure no-advance, explicit adapter, single-flight — progress invariants |
| `workflow_policy_projection_test.go` | REWRITE_FOR_RSG | `TestProjectStatusWorkflowPolicyStateMatrixUsesDeterministicCIProjection` | deterministic CI projection + no-retired-authority reads + revision projection |
| `local_code_inspection_perf_test.go *(build-tag liveperformance)*` | UNCLEAR | `TestLocalCodeInspectionPerformanceProfile` | liveperformance-tagged — timing-only, no recorded baseline |

**`internal/session`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `admin_session_test.go` | UNCLEAR | `TestAdminSessionIsDurableMachineScopedAndRevocable` | durable machine-scoped admin session — admin surface undecided |
| `store_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `store_tests_create_session_test.go` | REWRITE_FOR_RSG | `TestStoreSQLiteLifecycleHasNoSessionJSONAuthority` | unique-ID concurrent create, one-record bind, idempotent end — store invariants; legacy-untouched subpart drops |
| `store_tests_legacy_payload_test.go` | REWRITE_FOR_RSG | `TestStoreUpdateUsesStoredPayloadForLegacyRecordShape` | stored-payload CAS under stale reads — CAS invariant; legacy-shape subpart drops |
| `store_tests_session_test.go` | REWRITE_FOR_RSG | `TestTSK578LegacyIDsAreRejectedAndNotTranslated` | legacy-ID rejection — ID-format invariant |
| `tsk514_cutover_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK578LegacyCutoverDoesNotImportMalformedFiles` | GTW cutover no-malformed-import — one-time cutover proof |
| `tsk578_session_identity_test.go` | REWRITE_FOR_RSG | `TestTSK578SessionIDsUseCanonicalGatewayProjectRoleAndSuffix` | canonical session identity, role codes/schemas/binding — under new role model |
| `tsk672_monotonic_timestamps_test.go` | PRESERVE_INVARIANT | `TestTSK672SessionTimestampsSurviveWallClockSlew` | timestamps survive wall-clock slew/restart + corrupt-strict — durable-clock invariant |

**`internal/sqlitestore`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `callback_epochs_test.go` | UNCLEAR | `TestCallbackEpochRequiresRealWorkAndSurvivesRestart` | callback epoch durability — callback model undecided |
| `database_snapshot_test.go` | PRESERVE_INVARIANT | `TestSnapshotDatabasesIsOnlineConsistentAndRestorable` | online-consistent snapshot + oversized/symlink rejection — snapshot invariants |
| `databases_hard_cut_migration_test.go` | REWRITE_FOR_RSG | `TestFreshBaselinesAreTheOnlyMarkersAndReopen` | marker semantics + legacy/unknown-history fail-closed — bounded-migration mechanism invariants |
| `databases_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — shared fixtures (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `databases_tests_shared_migration_test.go` | REWRITE_FOR_RSG | `TestSharedMigrationHistoryPreservesReleasedVersionsBeforeCandidates` | released-version history preservation — migration-history invariant |
| `databases_tests_shared_server_test.go` | PRESERVE_INVARIANT | `TestOpenMigratesTwoIndependentStoresAndSharedCASIsAtomic` | two-store CAS atomicity + second-owner reject + lock/observer phases — store-open invariants |
| `local_operations_test.go` | PRESERVE_INVARIANT | `TestLocalOperationAllocationIsCompactMonotonicIsolatedAndRestartSafe` | compact monotonic isolated restart-safe allocation + concurrency — sequence/CAS invariants |
| `local_session_hard_cut_migration_test.go` | REWRITE_FOR_RSG | `TestMigrateNoncanonicalLocalSessionsIsBoundedDeferredAndIdempotent` | bounded deferred idempotent migration + over-bound reject — bounded-migration mechanism |
| `milestone_lifecycle_test.go` | REWRITE_FOR_RSG | `TestMilestoneSharedLifecycleDescriptorAndSchema` | descriptor + schema for milestone lifecycle |
| `project_configuration_activate_local_migration_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK606ActivateLocalInstallsOnCanonicalGTWConfiguration` | GTW activate-local procedure migration content |
| `project_configuration_activation_preflight_migration_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK627ActivationPreflightInstallsOnCanonicalGTWConfiguration` | GTW preflight procedure migration content |
| `project_configuration_e2e_migration_test.go` | UNCLEAR | `TestTSK693E2EProcedureInstallsAndCompiles` | e2e procedure install/converge/fail-closed — procedure-seed adoption undecided |
| `project_configuration_hard_cut_migration_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK666Gate20SharedConfigurationOutboxRevisionMigration` | GTW config hard-cut content (retired forms/gate mapping) |
| `project_configuration_procedure_schemas_migration_test.go` | UNCLEAR | `TestTSK687ProcedureSchemasConvergeBehindCompletedMarkers` | procedure-schema convergence/fail-closed — seed adoption undecided |
| `project_configuration_release_prod_migration_test.go` | DROP_LEGACY_COMPATIBILITY | `TestTSK529ReleaseProdInstallsOnCanonicalGTWConfiguration` | GTW release-prod procedure migration content |
| `project_retirement_test.go` | REWRITE_FOR_RSG | `TestProjectRetirementCancelsPendingConfigurationAndExcludesHistoryFromMigration` | retirement fencing + nonterminal block + history exclusion — lifecycle invariants |
| `session_bootstrap_grants_test.go` | REWRITE_FOR_RSG | `TestSessionBootstrapGrantIsDurableAndUniquePerProject` | durable unique-per-project grant — grant-store invariant under new bootstrap model |
| `shared_mutation_test.go` | PRESERVE_INVARIANT | `TestCommitSharedMutationCommitsEntityAndOutboxTogether` | entity+outbox atomic commit, idempotent identity-bound CAS, restart-safe outbox — CAS/atomicity core invariants |
| `shared_relation_outbox_test.go` | REWRITE_FOR_RSG | `TestSharedRelationInsertEnqueuesHubPublicationAtomically` | atomic outbox enqueue + storage-type reject + bounded backfill — outbox invariants; legacy-text-row subpart drops |
| `shared_sequence_hard_cut_migration_test.go` | REWRITE_FOR_RSG | `TestMigrateLegacySharedSequencesPreservesHighWaterAndDropsSources` | high-water preservation on sequence migration — sequence-integrity invariant |
| `shared_sequence_reconstruction_test.go` | PRESERVE_INVARIANT | `TestReconstructSharedEntitySequencesFromPortableCurrentState` | sequences reconstructable from portable current state — reconstruction invariant |
| `task_execution_locality_migration_test.go` | REWRITE_FOR_RSG | `TestTaskExecutionSharedToLocalMigrationPreservesBoundedState` | bounded preserve + conflicting-copy reject — locality invariant (single conservative class) |
| `tsk384_rule_seed_migration_test.go` | REWRITE_FOR_RSG | `TestTSK384RuleSeedMigrationFailsClosedWithoutCanonicalPolicy` | fail-closed w/o policy + bounded + atomic uniqueness — seed mechanism invariants |
| `tsk409_lifecycle_test.go` | REWRITE_FOR_RSG | `TestTSK409FreshV12SchemaAndRevisionOneHistory` | fresh schema + revision-one history + idempotent seed |
| `tsk409_rev7_lifecycle_event_test.go` | REWRITE_FOR_RSG | `TestTSK409Rev7ADRSummaryMigrationAddsValidatedTitleAndPreservesHistory` | summary migration validation/atomic/CAS-conflict — lifecycle-event mechanism invariants |
| `tsk480_task_priority_migration_test.go` | REWRITE_FOR_RSG | `TestTSK480PriorityMigrationClassifiesActiveLegacyValueWithRationale` | classify/bounded/terminal-preserve migration semantics |
| `tsk511_relation_authority_test.go` | REWRITE_FOR_RSG | `TestTSK511Gate20RelationAuthorityShape` | relation store atomicity/idempotence/pagination/materialization |
| `tsk521_task_execution_test.go` | REWRITE_FOR_RSG | `TestTSK521TaskExecutionStorageRejectsCorruption` | corruption rejection + single deterministic marker |
| `tsk531_summary_migration_test.go` | REWRITE_FOR_RSG | `TestTSK531SummaryMigrationPreservesLegacyPayloadAndAddsOneRevision` | payload preserve + one revision + conflict reject |
| `tsk531_task_lifecycle_authority_test.go` | REWRITE_FOR_RSG | `TestTSK531TaskLifecycleHardCutCopiesFieldExactlyAndCuts` | hard-cut field copy, atomic transitions, upgrade-from-old-store — lifecycle-authority invariants |
| `tsk531_task_sequence_migration_test.go` | REWRITE_FOR_RSG | `TestTSK531TaskSequenceMigrationRepairsAndPreservesAllocators` | allocator repair/preserve + project isolation |
| `tsk538_schema_assertions_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — assertion helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |
| `tsk580_token_usage_test.go` | UNCLEAR | `TestTSK580TokenUsageIsAtomicDeduplicatedAndBodyFree` | atomic dedup body-free usage — telemetry adoption undecided |
| `tsk585_task_completion_regression_test.go` | REWRITE_FOR_RSG | `TestTSK585TaskCompletionStoreValidation` | completion-store validation + history cursor ordering + lifecycle events |
| `tsk620_agent_identity_migration_test.go` | UNCLEAR | `TestTSK620NonterminalExecutionIdentityMigrationPreservesTerminalHistoryAcrossRestart` | agent identity preserve/collision — agent model undecided |
| `tsk623_local_operation_migration_test.go` | REWRITE_FOR_RSG | `TestTSK623ExistingLocalOperationsUpgradeBeforeAdmissionQuery` | upgrade-before-admission + convergence + failure-blocks-ready |
| `tsk649_project_bootstrap_test.go` | REWRITE_FOR_RSG | `TestTSK649ReconcileProjectBootstrapRepairsMissingLeavesWithoutReplacingExistingRule` | bootstrap reconcile repairs leaves w/o replacing — idempotent-repair invariant |
| `tsk664_gate20_table_inventory_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK664Gate20SharedAndLocalTablesAreClassified` | every store family classified — inventory gate re-proved on RSG schema |
| `tsk667_gate20_outbox_writer_inventory_test.go` | REPLACE_WITH_CONTRACT_HARNESS | `TestTSK667Gate20OutboxPayloadWriterInventory` | outbox-writer inventory — re-proved on RSG schema |

**`internal/tailcursor`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `tailcursor_test.go` | PRESERVE_INVARIANT | `TestCursorPagesMultipleNewLinesAndEmptyDelta` | cursor pages/window/ambiguity/scope/session-free — bounded-tail invariants |

**`internal/tokenizer`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `counter_test.go` | PRESERVE_INVARIANT | `TestCounterExactAndDeterministic` | exact deterministic offline counting + encoding rejection + parity fixtures — budget invariants |

**`internal/upgrade`**

| File | Class | Evidence (representative test) | Rationale |
|---|---|---|---|
| `fixture_test.go` | DROP_LEGACY_COMPATIBILITY | `TestPreviousVersionFixtureCoversUpgradeMatrix` | fixture coverage claim — GTW version fixtures |
| `inspect_hub_revision_test.go` | DROP_LEGACY_COMPATIBILITY | `TestInspectUsesExplicitHubRevisionWhenLocalStateCheckHasNone` | hub-revision inspection for upgrade — GTW upgrade path |
| `status_test.go` | REWRITE_FOR_RSG | `TestStatusNoHistory` | corrupt/symlink/noncanonical record rejection — artifact-record invariants |
| `upgrade_test_diagnostics_test.go` | REWRITE_FOR_RSG | `TestInspectConfiguredProjectsUsesManagedSnapshotAndRejectsMalformedRegistry` | sanitized bounded diagnostics + persist-before-rollback |
| `upgrade_test_release_test.go` | REWRITE_FOR_RSG | `TestValidateReleaseRequiresExactArtifactsAndChecksums` | manifest exactness/checksum/traversal rejection — artifact-manifest invariants |
| `upgrade_test_replace_failures_test.go` | REWRITE_FOR_RSG | `TestReplaceAllDirectorySyncFailureCleansStaging` | staging cleanup on failure — atomic-replace invariants |
| `upgrade_test_replace_test.go` | REWRITE_FOR_RSG | `TestReplaceAllStagesBeforeCommitAndRestoresCommitFailure` | stage-before-commit + per-position restore — atomic-replace invariants |
| `upgrade_test_rollback_test.go` | REWRITE_FOR_RSG | `TestRunnerRunSuccessfulRollbackProofClosure` | rollback proof closure + cleanup-failure retains backup |
| `upgrade_test_runtime_test.go` | REWRITE_FOR_RSG | `TestRollbackBackupCleanupPolicy` | lock contention/reacquire + version ordering rejection |
| `upgrade_test_success_test.go` | REWRITE_FOR_RSG | `TestRunnerRunSuccessProofClosure` | runner success proof closure |
| `upgrade_test_support_test.go` | DROP_LEGACY_COMPATIBILITY | `(no Test funcs)` | no Test functions — helpers (helpers/fixtures required by any kept proof must be rehomed/rebuilt — zero Test funcs ≠ zero harness dependency) |

### 8.1 Per-package test totals (derived from the ledger rows)

| Package | P | R | H | D | U | Total |
|---|---|---|---|---|---|---|
| cmd/gofmt-struct | 1 | 0 | 0 | 0 | 0 | 1 |
| cmd/gpt-tunnel | 1 | 7 | 13 | 2 | 0 | 23 |
| cmd/gpt-tunnel-gatewayd | 0 | 2 | 0 | 0 | 5 | 7 (2 active + 5 livee2e) |
| cmd/gpt-tunnelctl | 0 | 0 | 0 | 0 | 2 | 2 |
| internal/actioncontract | 2 | 0 | 0 | 0 | 0 | 2 |
| internal/activation | 0 | 3 | 0 | 0 | 6 | 9 |
| internal/airelay | 0 | 5 | 0 | 1 | 0 | 6 |
| internal/authority | 0 | 1 | 0 | 0 | 0 | 1 |
| internal/callbackdelivery | 0 | 0 | 0 | 1 | 0 | 1 |
| internal/config | 3 | 2 | 0 | 1 | 0 | 6 |
| internal/controller | 0 | 9 | 0 | 2 | 2 | 13 |
| internal/debug | 0 | 3 | 0 | 0 | 0 | 3 |
| internal/entity | 0 | 1 | 0 | 0 | 0 | 1 |
| internal/fsutil | 1 | 0 | 0 | 0 | 0 | 1 |
| internal/gates | 1 | 2 | 0 | 1 | 0 | 4 |
| internal/gitx | 6 | 2 | 0 | 1 | 0 | 9 |
| internal/gofmtstruct | 1 | 0 | 0 | 0 | 0 | 1 |
| internal/hub | 4 | 0 | 0 | 1 | 0 | 5 |
| internal/lockfile | 1 | 0 | 0 | 0 | 0 | 1 |
| internal/mcp | 9 | 48 | 5 | 12 | 20 | 94 (93 active + 1 liveperformance) |
| internal/model | 1 | 21 | 0 | 2 | 2 | 26 |
| internal/pagination | 1 | 0 | 0 | 0 | 0 | 1 |
| internal/publicprojection | 0 | 1 | 0 | 0 | 0 | 1 |
| internal/releaseartifacts | 0 | 1 | 0 | 0 | 0 | 1 |
| internal/runtime_log | 1 | 0 | 0 | 0 | 0 | 1 |
| internal/service | 11 | 77 | 5 | 15 | 13 | 121 (120 active + 1 liveperformance) |
| internal/session | 1 | 4 | 0 | 2 | 1 | 8 |
| internal/sqlitestore | 5 | 21 | 2 | 6 | 5 | 39 |
| internal/tailcursor | 1 | 0 | 0 | 0 | 0 | 1 |
| internal/tokenizer | 1 | 0 | 0 | 0 | 0 | 1 |
| internal/upgrade | 0 | 8 | 0 | 3 | 0 | 11 |
| **Total** | **52** | **218** | **25** | **50** | **56** | **401** |

## 9. Rejected / failed / corrected calls (evidence)

- `message/read GTW-MSG15`, `task/read GTW-TSK714`, `journal/read GTW-JRN42`, `journal/read
  GTW-JRN43` — succeeded (dispatch + spec sources).
- **Failed:** `go run ./cmd/gofmt-struct -check docs/` — exit 2, malformed invocation (flag
  applied to a docs path; gofmt-struct operates on Go source). No source effect.
- **Failed earlier command:** `ls /mnt/c/Users/` initially timed out/backgrounded (WSL mount
  latency) — retried successfully; Lead's frozen measurement files then read.
- **Lead-reported, carried verbatim:** Lead code/worktree failed resolving `refs/heads/main`
  before dispatch — no repair attempted; analysis ran in the assigned worktree.
- **v1 errors corrected in the prior revision:** entity/actioncontract/publicprojection/gitx
  wrongly labeled uncoupled KEEP (now REWORK with cited constants); "Hub authority is
  naming-only" claims removed and replaced by the §4 synchronous-coupling map; host/runtime
  DROP justified by file-level audit instead of inference; test ledger replaced guess-counts
  with per-path rows; workflowrole 40→97 lines; `durableMutationExecutionSet` filenames verified
  real (Lead's concern disproved by evidence — carried forward, no rename/delete); migration
  file counts now exact (38 prod / 21 test); `static-check.py` run recorded; per-package test
  counts corrected to frozen denominators; config row file names corrected; service HARNESS
  deduplicated to 5; hub split corrected to 4 PRESERVE + 1 helper; `apps_sdk_*` and
  `task_execution_locality_migration` each hold a single class.
- **v2 errors corrected in this revision (per GTW-MSG16):**
  - Direct deps now named (not "N internal") in §2.2/§2.3 and Appendix A; mcp direct internal
    edges corrected 16→**17**.
  - **False migrate-elimination claim fixed:** `shared_lifecycle_event.go:14` +
    `databases_{shared,local}_baseline.go` import `go-sqlite-store/migrate` for **current**
    schema DDL (`sharedLifecycleEventMigration` `:23-45`) — schema creation is interleaved with
    core code; the migrate edge stays until retained-table initializers are re-authored
    (§2.2/§6.2/§6.3/§6.4 amended).
  - Five `cmd/gpt-tunnel` live-harness tests moved DROP→REWRITE (disposable
    `testutil.NewLiveGateway`, not owner production; durable-boundary proofs remain).
  - Tagged livee2e restart/debug proofs, activation procedure/smoke/candidate tests and
    controller status/systemd tests moved DROP→UNCLEAR with concrete undecided-feature
    questions; "no Test funcs" helper rows now carry the explicit rehome/rebuild note; the
    unproven "duplicated elsewhere" claim removed.
  - Scripts counts exact (29+10=39); `static-check.py` reclassified REWORK (embedded GTW
    inventories); `go run` record corrected to tool-invocation-attempted wording.
  - Integration blocker recorded honestly in §10.
- No other rejected canonical calls; all file reads were read-only.

## 10. Submission evidence

- Sole repo change: `docs/migration/rsg-source-curation-inventory.md`.
- `git status` clean except that file; no source/test/config/contract/procedure/fixture/runtime
  change; no successor-repo, Hub, DB, Agent or Session operation performed.
- Bounded checks: `static-check.py` → `STATIC_CHECK_OK`; all measurements in §1 read-only;
  no test suite run (inventory work); no test or background processes left running.
- All classifications remain **nonnormative evidence** for Planner/Review per JRN42/43; open
  decisions (RSG role model, remote-authority model, agent/runtime-surface adoption, document
  formats, release/upgrade/activation features) are marked UNCLEAR rather than resolved here.
- **Integration blocker (honest record):** Lead's code/worktree still cannot resolve
  `refs/heads/main` after the rework (per GTW-MSG16); typed operator-root status is clean on
  branch `review/corrective-design-second-pass-20261004-2159` (head `a4e98ada`). The frozen
  inspected identity in §1 is the assigned **task base** — it is not claimed to be the current
  canonical main or operator checkout. No repair to refs/config attempted; no lifecycle
  verification is claimed; Lead owns canonical final verification/integration/Track submission.

## Appendix A — frozen package adjacency (Lead `go list -json`, verbatim)

Direct internal imports, direct external Go imports, and transitive external modules (from
frozen `Deps`, non-stdlib filtered) per package. Runtime dependencies are per §1.1, not in this
table.

| Package | Direct internal imports | Direct external Go imports | Transitive external modules |
|---|---|---|---|
| `cmd/gofmt-struct` | `internal/gofmtstruct` | — | — |
| `cmd/gpt-tunnel` | `internal/agentguide`, `internal/config`, `internal/controller`, `internal/gates`, `internal/gitx`, `internal/model`, `internal/publicprojection`, `internal/releaseartifacts`, `internal/service` | — | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk`, `github.com/rceman`, `gopkg.in/yaml.v3` |
| `cmd/gpt-tunnel-gatewayd` | `internal/authority`, `internal/config`, `internal/controller`, `internal/debug`, `internal/hub`, `internal/mcp`, `internal/model`, `internal/releaseartifacts`, `internal/service`, `internal/session`, `internal/sqlitestore` | — | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk`, `github.com/rceman`, `gopkg.in/yaml.v3` |
| `cmd/gpt-tunnelctl` | `internal/activation`, `internal/config`, `internal/controller`, `internal/fsutil`, `internal/releaseartifacts`, `internal/service`, `internal/upgrade` | — | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk`, `github.com/rceman`, `gopkg.in/yaml.v3` |
| `contracts` | — | — | — |
| `internal/actioncontract` | `contracts` | `gopkg.in/yaml.v3` | `gopkg.in/yaml.v3` |
| `internal/activation` | `internal/config`, `internal/controller`, `internal/fsutil`, `internal/mcpmanifest`, `internal/releaseartifacts`, `internal/sqlitestore` | — | `github.com/rceman` |
| `internal/agentguide` | — | — | — |
| `internal/airelay` | `internal/session` | — | `github.com/rceman` |
| `internal/authority` | `internal/session` | — | `github.com/rceman` |
| `internal/callbackdelivery` | — | — | — |
| `internal/config` | `internal/fsutil`, `internal/lockfile` | — | — |
| `internal/controller` | `internal/config`, `internal/fsutil`, `internal/lockfile`, `internal/releaseartifacts`, `internal/runtime_log` | — | — |
| `internal/debug` | `internal/activation`, `internal/config`, `internal/controller`, `internal/fsutil`, `internal/lockfile` | — | `github.com/rceman` |
| `internal/entity` | `internal/model`, `internal/pagination` | — | — |
| `internal/fsutil` | — | — | — |
| `internal/gates` | `internal/model`, `internal/tokenizer` | — | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk` |
| `internal/gitx` | `internal/config`, `internal/model`, `internal/pagination` | — | — |
| `internal/gofmtstruct` | — | — | — |
| `internal/hub` | `internal/config`, `internal/fsutil`, `internal/lockfile`, `internal/model`, `internal/runtime_log` | — | — |
| `internal/lockfile` | — | — | — |
| `internal/mcp` | `internal/actioncontract`, `internal/agentguide`, `internal/airelay`, `internal/authority`, `internal/config`, `internal/controller`, `internal/debug`, `internal/hub`, `internal/mcpmanifest`, `internal/model`, `internal/pagination`, `internal/publicprojection`, `internal/runtime_log`, `internal/service`, `internal/session`, `internal/sqlitestore`, `internal/tokenizer` | — | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk`, `github.com/rceman`, `gopkg.in/yaml.v3` |
| `internal/mcpmanifest` | — | — | — |
| `internal/model` | `internal/workflowrole` | — | — |
| `internal/pagination` | — | — | — |
| `internal/publicprojection` | — | — | — |
| `internal/releaseartifacts` | — | — | — |
| `internal/runtime_log` | `internal/lockfile`, `internal/pagination` | — | — |
| `internal/service` | `internal/actioncontract`, `internal/activation`, `internal/airelay`, `internal/authority`, `internal/config`, `internal/controller`, `internal/entity`, `internal/fsutil`, `internal/gates`, `internal/gitx`, `internal/hub`, `internal/lockfile`, `internal/model`, `internal/pagination`, `internal/publicprojection`, `internal/runtime_log`, `internal/session`, `internal/sqlitestore`, `internal/tokenizer`, `internal/workflowrole` | `github.com/rceman/go-sqlite-store/store` | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk`, `github.com/rceman`, `gopkg.in/yaml.v3` |
| `internal/session` | `internal/model`, `internal/sqlitestore`, `internal/workflowrole` | — | `github.com/rceman` |
| `internal/sqlitestore` | `internal/model`, `internal/pagination` | `github.com/rceman/go-sqlite-store/migrate`, `github.com/rceman/go-sqlite-store/store` | `github.com/rceman` |
| `internal/tailcursor` | — | — | — |
| `internal/testutil` | `internal/config`, `internal/sqlitestore` | `github.com/rceman/go-sqlite-store/store` | `github.com/rceman` |
| `internal/tokenizer` | — | `github.com/pkoukk/tiktoken-go` | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk` |
| `internal/upgrade` | `internal/activation`, `internal/config`, `internal/controller`, `internal/fsutil`, `internal/gitx`, `internal/lockfile`, `internal/releaseartifacts`, `internal/service` | — | `github.com/dlclark`, `github.com/google`, `github.com/pkoukk`, `github.com/rceman`, `gopkg.in/yaml.v3` |
| `internal/workflowrole` | — | — | — |
