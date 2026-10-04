# GTW Corrective Design — Independent Second-Pass Architecture Review

Status: Review evidence; non-normative
Review type: Independent, read-only
Date: 2026-10-04

Reviewed repositories:
- rceman/gpt-tunnel-gateway

Reviewed source:
- rceman/gpt-tunnel-gateway: b45cd3d18de8971e069c04a9d339bf634f914369

Additional reviewed state/revisions:
- Second-pass proposal snapshot, read from the existing local Hub mirror without fetching: d112f33348f03c7d50f1c6cf00adcf5aa90cb8f3
- GTW-ADR147, GTW-ADR148, GTW-ADR149: revision 1; proposed
- GTW-MIL2: revision 4
- GTW-TRK7: revision 2; GTW-TRK8, GTW-TRK9, GTW-TRK10: revision 1
- GTW-TSK699 through GTW-TSK712: revision 1; GTW-TSK713: revision 2
- Existing decision evidence: GTW-ADR72 revision 7; GTW-ADR142 revision 4; GTW-ADR144 revision 1
- Preserved first-review commit: 74ce9ba7f4b9260caa101e83205c6652218ff1d2
- First-review parent/product source: b45cd3d18de8971e069c04a9d339bf634f914369
- First-review file: docs/reviews/2026-10-04-systemic-architecture-review.md
- First-review file SHA-256: 009ad7710e510c6aaf73f7f06f0ca6493634ee8177b84c51598984cba945099a
- First-review frozen Hub revision: a2ca014a78f1bad0011acfa1f5e7039b83ba9f2e

Scope:
- Proposed ADR147–149 and their relationships with ADR72, ADR142, and ADR144
- F01–F13 corrective coverage, MIL2/TRK8–10/TSK700–713 decomposition and dependencies
- Shared/Local active authority, asynchronous Hub backup, explicit restore, lineage and RPS recovery
- Project principals, capabilities, owner bootstrap, delegation, revocation and actor separation
- Layered executable contracts, verification profiles, result reuse and test consolidation
- Truthful TSK699/TRK7 closure and historical TSK692 treatment

Provenance and interpretation:
- The complete A–N review below preserves the completed second-pass chat review as point-in-time evidence. This publication adds a provenance header, not new findings or revised conclusions.
- Observed facts are the pinned records, inspected source, and explicitly identified earlier measurements. Reviewer conclusions include the verdict, coverage statuses, blockers, and dispositions. Recommended edits, dependency graphs, proofs, and cutover plans are advisory.
- ADR147–149 were proposed in the reviewed snapshot, not approved normative architecture. Existing accepted ADRs remain decision evidence until separately superseded or revised through canonical authority.
- This artifact does not adopt recommendations or mutate product, lifecycle, database, Hub, Session, Agent, credential, release, or runtime state. Documentation publication is separate from the read-only investigation described below.
- Local execution abandonment reported by the owner was not independently reverified against live Local state. No full Go/race/performance baseline was rerun.

## A. Final verdict

**APPROVE_WITH_CHANGES**

Planner chose the correct three correction domains. The proposed architecture does not require a wholesale redesign, distributed consensus, enterprise IAM, or indiscriminate test deletion.

However, the proposals are **not ready for acceptance unchanged**. The principal problems are:

- lineage metadata is being asked to provide fencing it cannot provide;
- first-owner bootstrap and post-restore ownership are unspecified;
- the activation, authorization, and migration boundaries are not sufficiently precise;
- portable authorization introduces a cross-Track backup obligation;
- some essential migration work is scheduled after the mechanisms that require it;
- verification execution levels and their implementation owners remain ambiguous.

The smallest coherent correction is to retain the three Tracks, clarify the ADRs, split TSK702/704/711 at real proof boundaries, and move migration prerequisites into their actual cutover owners.

**Evidence scope:** I inspected ADR147–149 r1, MIL2 r4, TRK7–10, TSK699–713—including TSK713 r2—from the existing local Hub mirror pinned at `d112f33348f03c7d50f1c6cf00adcf5aa90cb8f3`. These records match the supplied design state. This is a proposal-snapshot review, not independent verification of current Local execution state.

The original artifact’s SHA-256 and commit remain unchanged. Targeted source inspection was limited to disputed boundaries. No full verification baseline was rerun; no repository, lifecycle, database, Hub, Session, Agent, or runtime state was changed.

## B. Blocking issues before ADR acceptance

### B01 — Lineage detection is not writer fencing

ADR147 requires rejection of silently diverging writable descendants but does not specify how the previous writer stops being writable.

An incarnation counter, parent snapshot, or publication CAS can detect or reject a conflicting **backup publication**. None prevents an offline old machine from continuing to commit locally.

Acceptance must choose a supported transfer model. My recommendation is:

- writable transfer requires canonical, durable surrender of the previous writer;
- unfenced restore may validate/import data but must not automatically activate a new writable authority;
- disaster recovery without source surrender requires an explicit weaker guarantee or a separate fencing decision.

Do not promise strong single-writer enforcement while also allowing an unfenced old writer to operate indefinitely without coordination.

### B02 — Project Admin has no specified genesis or restore-claim protocol

“Created through project bootstrap” is not a sufficient authorization rule.

Specify the trusted bootstrap actor, durable claim record, permitted project states, initial finite permission bundle, and post-restore claim process. A Hub credential, a role label, or ordinary Host Admin admission must not silently become project semantic authority.

### B03 — Active-authority cutover lacks a complete activation and writer boundary

The design needs one explicit active-project predicate and one final activation marker after durable Shared preparation and Local binding/authority preparation.

Task ownership must also cover:

- existing project/identifier mutation paths, not merely readers;
- retirement admission **and** retirement transition;
- startup usability and background publication scheduling;
- the interval in which replacement identity exists but old onboarding can still create projects without it.

Source still contains direct Hub transactions in project registration and identifier adoption. Removing readers alone does not remove authority.
<ref_snippet file="/home/therceman/git/gpt-tunnel-gateway/internal/service/service_project_mutations.go" lines="14-31" />

### B04 — Delegation and revocation need an enforceable security boundary

The ADR needs to close credential-issuance, runtime-rebinding, bulk-configuration, and Procedure-policy escalation paths—not only role-edit escalation.

Commit-point rechecking also needs a defined linearization point. “Check again” followed by an unguarded mutation still permits a revocation race, particularly with Shared bundles and Local assignments.

### B05 — Portable Role state has an insufficiently assigned round-trip owner

TSK707 introduces portable Role bundles. TSK702 primarily names Project identity and allocators. No acceptance criterion clearly assigns complete Role publication, restore, catalog-version validation, and inclusion in snapshot completeness.

This is a cross-Track obligation. It may reuse ProjectConfiguration publication rather than create another entity family, but it must have an explicit owner and executable round-trip proof.

### B06 — Existing normative decisions and actor-separation policy remain unresolved

Before acceptance:

- establish ADR147’s exact relationship with ADR144;
- explicitly partially supersede ADR142’s fixed-role clauses;
- revise ADR72’s mandatory Lead wording;
- decide the default GTW actor-separation policy without accidentally converting mandatory technical review into optional review.

ADR148 alone cannot silently reinterpret ADR72.

### B07 — Verification layering and prerequisite proofs are not assigned coherently

ADR149 distinguishes cheap Task coverage from Repository E2E, but TSK711 specifies a real-daemon harness without clearly owning the cheap exhaustive execution layer or profile runners.

Authority cutovers also need their failure proofs **before integration**, not after completion of a later verification Track.

Resolve the execution levels, profile ownership, and dependency graph before accepting this implementation decomposition.

## C. F01–F13 coverage matrix

Statuses assess **the plan as proposed**, not implementation completion. “Covered” means adequate planned decision/proof ownership; it does not mean the finding has been fixed.

| Finding | Decision | Corrective Tasks | Verification proof / final hard cut | Status |
|---|---|---|---|---|
| **F01: partial Hub-first bootstrap** | ADR147: Local-first creation, async backup, staged activation | 701, 702, 704, 705 | Hub-unavailable creation; interruption at each durable phase; failed publication does not undo creation; fragment never becomes active. Final removal: **706**. Phase/transfer rules need clarification. | **PARTIALLY_COVERED** |
| **F02: missing Shared Project authority** | ADR147: canonical Shared Project identity | 701, 703, 706 | Restart-safe migration; identity conflicts; active reads **and writes** without Hub; no second identity authority. Final removal: **706**. Writer ownership and transition interval are incomplete. | **PARTIALLY_COVERED** |
| **F03: backup existence selects restore** | ADR147: explicit restore intent | 704, 706 | Identical normal-onboard result with absent, complete, stale, or partial Hub data; explicit restore separately validated. Final removal: **706**. | **COVERED** |
| **F04: configuration/retirement remote veto** | ADR147: Shared retirement authority | 703, 706 | Conflicting Hub retirement cannot veto active Shared state; Shared tombstone blocks new admission and racing commits; cleanup resumes without Hub. Final removal: **706**. Transition proof needs explicit ownership. | **PARTIALLY_COVERED** |
| **F05: restore not visibility-atomic** | ADR147: staged restore, final activation | 702, 704 | Corrupt later family, interruption, concurrent admission, Local preparation failure, replay and restart never expose partial active state. Final removal: **706**. Activation predicate and phase mechanics remain unspecified. | **PARTIALLY_COVERED** |
| **F06: incomplete portable symmetry** | ADR147 + ADR148 portability | 701, 702, **707**, 704, 706 | Full portable cut, including Role state, history/relations, and allocator reservations; strict round trip; no runtime restoration. Final storage inventory: **706**, coordinated with TRK9. | **PARTIALLY_COVERED** |
| **F07: fixed names encode topology** | ADR148: capabilities and eligible assignment | 707–710; ADR72 revision | Arbitrary names, no Lead/Worker name, multiple runtimes, combined orchestration/execution, actor-separation policy. Final removal: **710**. Current migration timing is too late. | **PARTIALLY_COVERED** |
| **F08: advertised role authority not reliably enforced** | ADR148: one permission predicate | 707, 708, 710, 711 | Discovery/invocation agreement; just-sufficient and insufficient grants; cross-project denial; Procedure and credential-escalation cases. Final removal: **710**. Missing escalation boundaries must be specified. | **PARTIALLY_COVERED** |
| **F09: Sessions lack universal live revocation** | ADR148: current principal authority | 708, 710, 711 | Next-admission revocation; queued work; role edits; commit/revocation race with a defined winner. Final removal: **710**. Atomic enforcement semantics are incomplete. | **PARTIALLY_COVERED** |
| **F10: listener ready ≠ usable project** | ADR147: runtime/backup/restore separation | 703, 704, 706 | Restart existing projects with unavailable/locked Hub; usable Session/runtime actions; publication retries and independent health reporting. Final removal: **706**. Startup/outbox ownership must be added explicitly. | **PARTIALLY_COVERED** |
| **F11: descriptive/self-derived inventory proof** | ADR149: executable contracts and unique proof owners | 711, 713, plus 706/710 | Observed effects and denials, not description strings; exact enabled inventory; independent authority/failure witnesses. Final descriptive-proof retirement: **706, 710, 713**. Harness levels and cross-Track prerequisites need edits. | **PARTIALLY_COVERED** |
| **F12: broken candidate live E2E** | Existing ADR72/139 requirements; reinforced by ADR149 | 700 | Both tests actually execute against the exact candidate; valid configuration/bootstrap; bounded diagnostics; real restart/activation. No architectural hard cut required. | **COVERED** |
| **F13: incomplete broader verification identity** | ADR149: material result identity, separate admission | 712 | Source/profile/toolchain/environment invalidation; failed/incomplete outcomes rejected; fresh Task/base/review admission. Final old receipt interpretation replaced by **712**. Key construction should be simplified. | **COVERED** |

There are **zero unexplained findings**, but several are only partially covered. Merely attaching every finding number to a Task is not sufficient acceptance evidence.

## D. ADR147 disposition

**EDIT + SUPERSEDE_RELATION_REQUIRED**

### Recommended relationship

Choose:

**ADR147_SUPERSEDES_ADR144**

ADR144 is directionally correct. ADR147 adds materially stronger decisions about active authority, explicit restore, readiness, retirement, and writable transfer. Keeping both as overlapping current authority documents is unnecessary.

Before acceptance, ADR147 should:

- explicitly supersede ADR144;
- carry forward every surviving portability, no-runtime-restoration, strict-decoding, canonical-representation, and bounded-hard-cut invariant;
- incorporate Role definitions/bundles by reference to ADR148;
- preserve ADR144 as historical evidence.

Do not supersede ADR144 while inadvertently losing its stricter portability clauses.

### Active authority

Approve the proposed boundary:

```text
Shared = active portable project semantics
Local  = machine/runtime bindings and authority
Hub    = asynchronous backup and explicit restore input
```

I found no semantic domain that requires Hub to remain synchronously authoritative during ordinary active operation.

Source repositories remain authoritative for commits and trees. Explicit source fetch/push requirements are not Hub semantic authority.

Project identity changes, identifier operations, retirement, completion, configuration, Session admission, and runtime admission must use the replacement authority. They cannot remain Hub-first exceptions.

### Minimum Project identity

Canonical Shared identity is necessary because active behavior currently depends on identity stored elsewhere. It does **not** require a large new aggregate.

| Candidate data | Recommendation |
|---|---|
| Project identifier and canonical code | Required; preserve existing identity and consumed identifier semantics. |
| Repository identity | Required as one canonical typed identity. Do not require a remote API lookup for ordinary validation. A Local filesystem path is not portable repository identity. |
| Default branch | Retain only as required portable project policy, under **one** Shared owner. It need not be duplicated inside identity and configuration or treated as immutable repository identity. |
| Semantic revision | Use the existing entity/CAS revision mechanism. Do not add a project-wide revision incremented for every domain mutation merely for symmetry. |
| Lineage/incarnation | Keep only the origin/transfer metadata needed for the supported writer-transfer contract. Do not add two redundant “project UUID” and “lineage UUID” fields without distinct purposes. |

A slug, repository name, or three-letter code alone cannot distinguish independently created authorities sharing those labels. If current identifiers cannot provide that distinction, one stable opaque origin identifier is justified.

Neither globally unique human naming nor coordinated creation across independent offline stores can be guaranteed by local checks alone. Backup publication must reject conflicting roots rather than silently coalesce them.

### Single writable authority

The current lineage concept is **necessary for identifying divergence but underspecified and insufficient for fencing**.

The smallest strong supported transfer is:

1. The source stops new writes and drains or cancels outstanding writes.
2. It durably records writer surrender so restart cannot resume the old writable binding.
3. It produces a complete final checkpoint and a transfer record bound to the project, source generation, checkpoint, and selected destination.
4. The destination imports that checkpoint and activates the next writer generation.
5. Publication rejects an old generation or unrelated origin rather than overwriting the new backup lineage.

This requires coordinated owner-controlled handoff, not distributed consensus.

For the requested scenario:

```text
A active → backup → B restores → A resumes
```

a backup by itself gives B **no evidence that A surrendered**. B must not automatically become writable from that backup.

If A cannot surrender, the design must either:

- keep the restored project non-writable pending an adequate fencing decision; or
- explicitly define an owner-authorized disaster-recovery mode with weaker guarantees and observable fork risk.

A publication conflict discovered later is useful containment, but it is not prevention of concurrent local writing.

### Normal onboarding and explicit restore

Approve:

```text
validate Local repository and Local/Shared conflicts
→ prepare Shared semantics
→ prepare Local binding and initial authority
→ activate
→ publish asynchronously
```

Normal onboarding must not inspect Hub to infer create versus restore.

Restore must be semantically explicit. Its CLI spelling is not architecturally important.

A new-project request and an existing-project restore are different assertions. Backup existence must never silently reinterpret either.

### Minimum durable activation phases

Use a durable operation identity and three meaningful states:

1. **Preparing:** exact project/repository identity and selected snapshot, if any, are recorded; semantic data and Local preparation are not active.
2. **Ready:** complete staged semantics are validated; allocator state, Local binding, owner claim, and required transfer evidence are durable.
3. **Active:** one final Shared activation transition publishes the complete project and publication intent.

Local preparation happens before final Shared activation. The active resolver requires matching prepared Local binding/authority and the Shared activation generation.

This avoids a distributed transaction coordinator:

- pre-activation failures remain staged;
- post-activation restart reconciles from the durable activation marker;
- missing or mismatched Local preparation fails closed;
- staged records never appear through ordinary active-project enumeration or admission.

Restore replay must also protect against **same-revision different-content** and concurrent import/mutation races. A pre-read equality check followed by an unconditional equal-revision write is not sufficient.

### Backup completeness

Prefer the existing Git commit as the atomic container plus **one versioned manifest**.

It should identify the portable format/project/authority generation and bind:

- required portable families, histories, relations, and relevant immutable evidence;
- allocator high-water and reservation evidence;
- a consistent semantic cut;
- validated content membership or digests.

Pin the Git revision before reading it. The manifest need not contain its own enclosing commit SHA; its membership in that pinned immutable commit supplies that binding.

Do not add a separate commit marker, snapshot database, and second manifest unless each proves something distinct.

Incremental publication is acceptable, but a checkpoint becomes restore-eligible only when it represents a consistent completed cut. A collection of independently current files is not automatically such a cut.

### Retirement and startup

Approve:

```text
Shared retirement barrier/tombstone = semantic authority
Local quiescence/cleanup           = resumable execution consequence
Hub retirement                    = asynchronous evidence
```

Admission and racing commit points must respect the Shared retirement barrier. Failed cleanup or publication must not resurrect authority.

Approve separate states for:

- active runtime usability;
- backup health and lag;
- restore availability.

Assign startup/outbox changes to TSK703. Current startup launches the outbox worker only after Hub ensure/reconciliation succeeds; changing action readers alone does not cover that sequencing.
<ref_snippet file="/home/therceman/git/gpt-tunnel-gateway/cmd/gpt-tunnel-gatewayd/main.go" lines="168-180" />

## E. ADR148 disposition

**EDIT. Keep the principal/capability model.**

### Initial Project Admin bootstrap

Use the current trusted local owner/operator boundary for a **bounded genesis/claim operation**, not a new portable human identity system.

1. **Who creates the first Project Admin?**
   The authenticated local owner explicitly provisioning or claiming that project—not a role name, Hub credential, or unauthenticated project request.

2. **What durable authority proves it?**
   A Local provisioning/claim record bound to project origin, activation generation, repository/snapshot identity, operator provenance, initial principal, and finite approved capability assignment.

3. **What happens after restore?**
   Portable roles return. Source-machine principals, assignments, grants, Sessions, and credentials do not. The restored project remains staged/unclaimed for privileged writable use.

4. **How does the owner reclaim authority?**
   Through the bounded claim operation after restore and transfer validation. It creates a fresh Local project principal and server-issued bootstrap grant. It does not impersonate an historical actor.

5. **Is portable owner identity required?**
   **No, not for the current single-user owner-controlled deployment.** Add it only if authenticated ownership continuity across different people/hosts becomes a requirement.

The claim path must not become a standing generic Host Admin bypass into every already-claimed active project. Recovery of an already-claimed project is a separate explicit, audited operation.

The first owner may receive a generated ordinary Role bundle containing the finite approved project permissions. Its **contents**, not its name, provide authority.

### Portable versus Local authorization

Keep this boundary:

| Portable Shared semantics | Local live authority |
|---|---|
| Role definitions and finite capability bundles | Concrete principals and enabled state |
| Project authorization/separation policy | Principal role assignments |
| Catalog/schema compatibility metadata | Credentials, Session grants, Sessions |
| Historical actor and authority-change provenance needed for semantic history | Runtime bindings and direct operational authority |
| Relevant immutable review/acceptance attribution | Task/runtime leases and execution identity |

**Assignments may remain Local initially.** Restoring a `release-manager` role with no authenticated assignee is correct and safer than resurrecting an Agent.

Portable historical attribution is not permission continuity. Stable historical actor references must remain distinguishable from newly authenticated principals even when names match.

Reusing a physical runtime across projects requires explicit project-scoped principal/binding authority. It does not permit using a GTW Session as an RPS Session. The existing Agent model is project-scoped.
<ref_snippet file="/home/therceman/git/gpt-tunnel-gateway/internal/model/agent.go" lines="32-41" />

### Capability granularity

Use a finite catalog at semantic boundaries. Do not mirror every endpoint.

Critical separations that should remain explicit are:

- project read, configuration, and retirement;
- authority administration/delegation;
- runtime management versus prompting/messaging where they have different effects;
- Task authoring, dispatch, execution, submission, technical review, verification, integration, and semantic completion;
- Track authoring versus acceptance;
- ADR authoring versus acceptance;
- Procedure invocation versus authority to approve its privileged purpose;
- all project permissions versus Host administration.

Ordinary CRUD variants within one boundary need not have separate permissions. `submit-code`, `submit-tests`, and `submit-rebase`, for example, can share a submission capability while core phase/assignment invariants remain enforced.

Use one canonical action-to-capability mapping, including operator classification and dynamic Procedure requirements. Approve the exact initial catalog before its public schema is implemented.

### Multiple roles, grants, denies, inheritance

- **Multiple roles: approve.** Effective permissions are a simple union.
- **Direct grants: omit initially.** Owner bootstrap and narrow permissions can use ordinary bundles. Current requirements do not establish a material advantage sufficient to require a second assignment form.
- **Direct denies: omit.**
- **Role inheritance DAGs: omit.**

Direct grants can be added later if concrete role proliferation demonstrates their value.

### Delegation and escalation

Require the issuer to have:

1. authority-management permission; and
2. every capability being delegated.

“Cannot delegate what you possess” does not mean that every principal may delegate everything it possesses.

Treat all of these as authority-changing operations:

- role and assignment changes;
- issuing credentials for another principal;
- changing Session/principal binding;
- runtime identity rebinding;
- bulk configuration changes containing authorization policy;
- changes to Procedure permission requirements or approval policy.

A lower-privilege principal must not mint a token for a more privileged principal or attach its runtime identity to that principal.

Role edits must evaluate effects on **all current assignees**, including the editor. Adding capabilities to an assigned bundle is a delegation to those assignees. Editing an unassigned bundle must not create a latent bypass around later assignment checks.

Reject actual self-escalation; do not unnecessarily prohibit edits that leave the editor’s effective authority unchanged.

### Procedure invocation

A broad `procedure.invoke` permission must not become a universal approval or Host-command permission.

Require:

- an approved Procedure definition;
- its declared/core-purpose permission requirements;
- project scope;
- exact definition identity;
- additional Host authorization or bounded Host-owner delegation when the effect is Host administration.

Protect authorization-affecting Procedure metadata from ordinary configuration changes.

The existing `e2e` Procedure has a special Planner authorization path. Its approval protection must survive as semantic authority/policy, not merely disappear with the string check.
<ref_snippet file="/home/therceman/git/gpt-tunnel-gateway/internal/mcp/generic_procedure_actions.go" lines="40-66" />

### Live Session authority and TOCTOU

Approve current capability resolution rather than frozen role authority.

Minimum implementation:

- authenticate principal/project at every admission;
- resolve current Local assignments and Shared bundle revisions;
- invalidate or version any cached effective authority;
- reauthorize queued work before execution and before privileged commit;
- serialize authority mutation and the protected commit, or compare authority versions under an equivalent enforced commit guard.

A check followed by an unguarded commit is insufficient.

For external irreversible effects, define the durable authorized commit-intent boundary. Revocation before that boundary blocks the effect; an effect already irreversibly admitted may finish or reconcile. Do not promise cancellation of an external effect after it has already occurred.

### Roles and distinct actors

Approve capability-based execution with exact assignment/lease and an eligible Local runtime. No mandatory `worker` name is needed.

Approve capability-based orchestration and ordinary authorized Review principals. No mandatory `lead` or `review` name is needed.

However:

- technical review remains mandatory where ADR72 requires it;
- independent-review policy compares principal identity, not Session ID or role name;
- changing credentials or taking another role does not create an independent actor;
- preserve GTW’s intended implementation/review separation by default until explicitly revised.

A single principal may orchestrate and implement. That does not automatically authorize it to provide an independent review of its own work.

## F. ADR149 disposition

**EDIT. Choose layered execution.**

### Completion contract versus evidence profiles

Keep the distinction explicit:

```text
ADR72 Gates 1–20 = semantic completion/review obligations
Profiles        = ways to obtain evidence for those obligations
```

A successful profile is not automatic semantic approval. A budget miss is not authorization to skip evidence. There is no Gate 21.

### Complete Action execution level

Choose:

**LAYERED**

1. **Exhaustive in-process public-boundary contracts for the Task profile.**
   Requests enter the actual decoder/router, contract validation, Session/principal resolution, authorization, handler, and output projection. They use real disposable stores and controlled workers.

   Bare service calls with injected “authorized” context do **not** satisfy this layer.

2. **Small real-daemon transport-equivalence and failure witnesses.**
   Cover HTTP/MCP framing, credentials, bootstrap, discovery, middleware, workers, readiness, process identity, restart, recovery, cancellation, and representative effects.

3. **Complete real-daemon contract replay at Repository E2E/release boundaries.**
   Reuse the same expert-maintained fixtures through a daemon adapter. Run affected real-daemon scenarios earlier for significant boundary changes.

This provides exhaustive semantic coverage, genuine process-boundary proof, and better failure localization without making every Task repeat the broadest environment.

It is not established that 119 real-daemon Actions are intrinsically expensive. Shared startup can amortize their cost; Git/SQLite/operation fixtures may dominate either adapter. Measure representative execution when implementing the harness. Do not claim a speedup now.

### Inventory and fixture requirements

For each **enabled** Action, require:

- valid request and meaningful structured response;
- applicable invalid-schema cases;
- allowed and denied authority contexts;
- durable effect, or explicit read-only/no-effect expectation;
- idempotency, replay, continuation, or explicit non-applicability.

Invalid and denied calls should also prove absence of unintended effects.

Apply inventory equality to:

- normal static Actions;
- enabled debug Actions under their profile;
- dynamic Procedure Actions for the selected configuration;
- operator/bootstrap routes;
- anonymous/sessionless surfaces.

The baseline `119` is not the invariant. The compiled enabled inventory is.

For dynamic Procedures:

- framework fixtures prove compilation, schemas, permissions, and execution boundaries;
- owner-maintained project fixtures prove the configured Procedure’s meaning;
- the fixture/reference binds its definition digest;
- a generic schema fixture cannot certify arbitrary Procedure semantics.

Sessionless routes require their actual authentication expectations—token redemption, protected operator credentials, or intentional anonymous access—not artificial “missing Session” denials.

### Generated versus maintained expectations

Approve schema-derived checks for required fields, types, bounds, enums, unknown fields, closed objects, output validity, and inventory equality.

Expert-maintained fixtures must own:

- meaningful values;
- lifecycle transitions;
- durable effects;
- authorization;
- replay/idempotency;
- source and artifact relationships.

Normalize generated identifiers/times only while preserving relational correctness. Do not normalize revisions, source digests, permission decisions, or missing useful output into invisibility.

### Internal tests

Retain uniquely useful proofs for the first review’s listed invariants. Additionally make ownership explicit for:

- staged-project invisibility and activation recovery;
- checkpoint consistency and old-writer rejection;
- retirement versus concurrent admission/commit;
- Session/grant/runtime impersonation;
- Procedure-purpose authorization;
- atomic authorization version checks;
- cancellation and outcome-unknown external effects;
- queue shutdown, resource cleanup, and bounded retry behavior.

Do not repeat every such case through every API. Assign each invariant one primary proof owner and public witnesses where needed.

### Profiles and duplicated work

| Profile | Recommended purpose |
|---|---|
| **Developer** | Cheap static/compile/inventory checks, affected contracts and deterministic invariants. |
| **Task** | Complete cheap public-boundary matrix, applicable internal proofs, and affected daemon witnesses. |
| **Repository E2E** | Complete daemon replay plus lifecycle/failure/restore/restart/migration scenarios. |
| **Race** | Relevant instrumented concurrency/storage/authority corpus; broader release/periodic runs. |
| **Performance** | Applicable latency/resource/cold-warm scenarios, with separate measurement identity. |

Do not require both the canonical sharded full runner and direct complete Go runner as routine duplicate proof. The earlier sequential audit batch was measurement work, not a target Task profile.

Race instrumentation and genuine daemon replay can justify repeated semantic cases because they prove additional properties. Merely running the same uninstrumented suite twice does not.

The proposed 60-second, 90–180-second, and ten-minute budgets remain **targets**. Preserve current trustworthy evidence until measured replacements exist; do not enforce targets by suppressing cases.

### Reuse identity

Use one inspectable result manifest, not a large collection of independently maintained hashes.

| Proposed identity | Treatment |
|---|---|
| Repository/project | Required logical scope where behavior is scoped; not absolute worktree location. |
| Exact source commit/tree/content | Required authoritative artifact identity. Derive tree from Git; require a clean committed candidate for authoritative Task evidence. Developer dirty-content runs need an explicit content identity. |
| Verification definition/profile | Required, including runner and selected scenario scope. |
| Test/fixture inventory | Required coverage identity, but may be covered by source/profile digest rather than another filesystem scan. |
| Contract/schema digest | Required material identity; fold into the definition manifest when already derived from pinned source/configuration. |
| Flags/build tags | Required, including relevant execution modes. |
| Dependency locks | Required material input, normally already bound by the source tree. |
| Toolchain/executable | Required versions/content identity for Go, candidate executable, and materially involved external tools. |
| Environment/profile | Required curated non-secret material properties, not every environment variable or host detail. |

**Missing clarifications:**

- selected configuration/Procedure/authorization-policy identity when not already source-bound;
- immutable verification scope and expected executed inventory;
- result producer/provenance and trusted receipt admission;
- complete outcome sealing and freshness rules;
- test-affecting external dependencies, or hermetic replacements.

Avoid hashing entire home directories, live databases, all environment variables, or secrets. Transient fixture ports/paths are not normally cache identity. CPU model is not routinely required for semantic proof, but performance evidence needs a relevant machine profile.

Keep raw exact-artifact proof separate from current Task/review/base/assignment/policy admission. Task changes need fresh admission; they need not force unrelated expensive proof to rerun when its exact material identity still matches.

Production activation and readiness remain fresh observations. Go’s package cache is an optimization, not an authoritative GTW receipt.

## G. Existing ADR treatment

| Record | Exact recommended treatment |
|---|---|
| **ADR72** | **PARTIAL_ADR72_REVISION_PLUS_ADR149.** Preserve all twenty gates, numbering, fail-closed semantics, exact-artifact review, and before/after implementation obligations. Replace role application with authorized implementation, technical-review, and Track-acceptance principals. Technical review remains mandatory. Actor independence is explicit policy; preserve GTW’s current intended separation by default. ADR149 owns evidence execution architecture only. |
| **ADR142** | **PARTIALLY_SUPERSEDE_WITH_ADR148.** Preserve server-issued opaque grants, token-only redemption, runtime key ≠ public Session, project isolation, daemon-owned durability, Session-free managed CLI, same-user operator practicality, and Host/project separation. Supersede fixed role enums, hierarchical Planner→Lead→Worker delegation, role-frozen authority, and role-derived routing. Bind grants to principals/project and approved delegation instead. Do not add TTL or one-time-use requirements incidentally. |
| **ADR144** | **ADR147_SUPERSEDES_ADR144**, after all surviving clauses are carried forward. One current storage/backup authority contract; historical ADR144 remains intact. |
| **TSK692** | Preserve the historical Task and evidence. Specifically supersede **implicit create-or-adopt behavior** through accepted ADR147 and its implementing Tasks. Rewrite current tests that require automatic adoption. Retain identity conflicts, allocator non-regression, explicit portable restore, and no-source-topology restoration. Do not relabel the historical Task as incorrectly executed merely because its approved semantics are now being replaced. |

Recommended ADR72 replacement meaning:

> Before integration, an authorized technical-review principal evaluates Gates 1–20 against the exact final candidate and project evidence. Scripts do not replace semantic review. Required actor independence is enforced against principal identity under the approved project policy. Track acceptance is performed by a principal authorized for that separate decision.

This changes authority naming, not review sufficiency.

## H. Task-by-task disposition

Suffixes below describe recommended boundaries, **not newly created lifecycle entities**.

| Task | Disposition | Required scope |
|---|---|---|
| **TSK700** | **EDIT** | Keep the bounded existing-verification repair. Explicitly cover retired Session/bootstrap setup as well as branch collision and GatewayID. Both tests must execute against the exact candidate; no skips or product-semantic changes. Record diagnostics and timing. |
| **TSK701** | **EDIT** | Correct first semantic foundation. Define the minimum identity and origin/transfer requirements first. Migrate validated existing identity without allocator drift. Specify the pre-cutover state and how identity mutations are fenced until reader/writer cutover. |
| **TSK702** | **SPLIT** | **702A:** canonical Project/identifier/allocator export and publication, preserving reservations/high-water and async failure semantics. **702B:** complete checkpoint/manifest, consistent-cut validation, strict staged restore reader, and portable round-trip proof—including Role state. No activation or Local topology creation here. |
| **TSK703** | **EDIT** | Own existing active reads, relevant existing identity/identifier writers, retirement transition/admission, startup usability, and publication-worker scheduling. Run after replacement/checkpoint proof, before normal-onboard cutover. Disable obsolete creation entry points if there is an intermediate interval without a replacement. |
| **TSK704** | **SPLIT** | **704A:** normal Local-first create, shared activation protocol, Local owner/binding preparation, exact retry, crash recovery. **704B:** explicit snapshot selection/import, transfer/claim validation, restore-specific recovery, and activation using the same protocol. Do not build two activation engines. |
| **TSK705** | **EDIT** | Move real RPS action after authority hard cuts and incident-specific disposable proof. Separate semantic onboarding success from optional relay/runtime attachment. “No duplicate Lead” must mean no unnecessary duplicate physical runtime—not a ban on a required RPS-scoped principal/binding. |
| **TSK706** | **REORDER** | Final storage hard cut before real RPS recovery, not after it. Retire remaining obsolete APIs/helpers/current descriptions and perform the systemic inventory. Any actively used old authority must already have been removed by its cutover owner; this Task cannot leave reachable fallback behavior until the end. |
| **TSK707** | **EDIT** | Own catalog/mapping, portable bundles, Local principals, initial-owner/claim primitives, default templates, and Role publication/import proof. Prepare deterministic existing-identity mapping. Omit required direct-grant support initially. |
| **TSK708** | **EDIT** | Own the actual existing principal/grant/Session migration **before** activating live capability authorization. Include credential/runtime escalation guards, generalized bootstrap boundaries, and commit-point enforcement. Unmapped/ambiguous identities fail closed; no old-role fallback. |
| **TSK709** | **EDIT** | Keep eligible assignment/routing. Include review/approval actor semantics and Procedure-purpose checks where routing currently embeds them. Support combined orchestration/execution without implicitly granting independent self-review. |
| **TSK710** | **EDIT** | Keep final fixed-role hard cut/inventory. Move first usable defaults/migration into 707/708. Do not postpone bootstrap/schema changes needed by 708/709 until this Task. Preserve historical labels only as non-authoritative data. |
| **TSK711** | **SPLIT** | **711A:** shared fixture manifest, in-process public-boundary runner, daemon adapter, explicit profile runners/selection, and representative boundary fixtures. **711B:** exhaustive final enabled inventory, expert semantic expectations, final capability cases, dynamic/operator/anonymous inventories, and full daemon replay. |
| **TSK712** | **REORDER** | Depends on stable profile/manifest identity from 711A, not completion of every Action fixture in 711B. Implement trusted raw results versus current admission and curated material identity. Unfinished coverage cannot be represented as exhaustive proof. |
| **TSK713** | **EDIT** | Consolidate only after replacement proof for the affected cluster exists. Require per-deletion proof ownership and comparable measurements. Do not wait for 712 where fresh verification already suffices; retain final profile measurement ownership here. |

TSK702’s concepts are related, not four unrelated Tasks. Two boundaries are enough: portable publication versus restore-eligible consistent checkpoint.

TSK704 is too large as written because normal creation and explicit restore have different input, ownership, and failure proofs. Two Tasks sharing one activation mechanism are enough.

## I. Track disposition

**Keep TRK8, TRK9, and TRK10.**

They are good architectural ownership and Planner acceptance boundaries:

- **TRK8:** active storage, backup, restore, transfer, retirement, and RPS recovery;
- **TRK9:** principal authority, delegation, revocation, execution eligibility, and actor policy;
- **TRK10:** executable inventory, evidence profiles, receipts, and consolidation.

They are **not independent deployment boundaries**.

TRK8 acceptance must account for portable authorization and restored owner claim. TRK9 acceptance must account for project origin/activation scope and role round trips. TRK10 supplies prerequisite proof infrastructure to both.

Do not require whole-Track completion before consuming a reviewed foundation Task. Conversely, do not accept a Track with unresolved cross-Track obligations merely because all its local Tasks are green.

MIL2’s current membership of TSK699 needs truthful supersession treatment, not a fabricated successful completion.

**Review-artifact integration:** keep the raw commit immutable. If a main-tree readable copy is needed, a separate docs-only normalized derivative with raw commit and SHA-256 provenance is appropriate. Restrict normalization to the documented whitespace changes and label the derivative non-normative. No copy is necessary merely to erase a warning; a provenance reference can suffice. This does not justify changing global Git/verification policy or touching the raw artifact.

## J. Cross-Track dependency graph

`D` means accepted edited ADR147–149 plus their explicit ADR72/142/144 treatments.

```text
Existing requirements ─────────────────────────────→ 700

D → 701
D → 711A

701 → 702A
701 → 707

707 + 702A → 702B

707 + 700 + 711A
    → 708
    → 709
    → 710

702B + 700 + 711A
    → 703

703 + 708
    → 704A

702B + 708 + 700 + 711A
    → 704B

703 + 704A + 704B
    → 706

706 + 708 + incident-specific disposable proof
    → 705 semantic RPS recovery/onboarding

705 activation + 709/710
    → optional RPS runtime attachment

706 + 710 + 704A + 704B
    → 711B final exhaustive inventory/replay

711A → 712

711B + replacement proof for affected clusters
    → 713
```

Additional rules:

- 701/707 construction can begin without waiting for 700, but irreversible migration/cutover deployment requires healthy applicable live proof.
- 703 and 708 can progress in parallel after their prerequisites; they share project-scope interfaces and must coordinate changes to Session/runtime admission.
- 704B can progress independently of 704A once the shared activation primitive/interface is established.
- Each authority Task supplies its own affected fixtures and failure cases using 711A. It cannot wait for 711B to test its cutover.
- RPS must not wait for unrelated test consolidation or cache work if required evidence is collected freshly.
- 711B waits for final interfaces to declare final-model coverage. Its foundation and individual fixtures should land earlier.
- 712 may run alongside authority work. Its receipts bind the actual profile/source inventory; they cannot claim future coverage.

Thus **TRK8 and TRK9 can execute partly in parallel**, but capability state needs canonical project scope from 701, and final writable bootstrap/restore needs the claim/live-authority foundation from 707/708.

TSK711’s foundation does not need final capability cutover. Its final authorization fixtures do.

## K. TSK699 / TRK7 truthful closure

Recommended semantics after the architecture decision:

1. Preserve TSK699’s original contract, failed/abandoned execution history, and dispatched provenance.
2. Confirm canonically that no nonterminal execution, lease, callback, or admission can resume it.
3. Archive TSK699 with an explicit reason: its Hub-driven contract is obsolete under ADR147; replacement intent is implemented by the new authority/recovery work.
4. Close TRK7 as **cancelled because superseded/abandoned**, retaining `dispatched_tasks`, historical membership, actor, reason, and replacement references.
5. Archive that cancelled Track if desired.
6. Ensure MIL2’s current required scope treats the old work as superseded—not as successfully integrated.

Task archival already supports planned Tasks after checking for nonterminal execution.
<ref_snippet file="/home/therceman/git/gpt-tunnel-gateway/internal/service/task_lifecycle.go" lines="80-107" />

**There is a separate lifecycle design defect:** Track cancellation is currently planned-only, while archival accepts only accepted/cancelled Tracks. An active abandoned Track therefore lacks this truthful canonical closure path.
<ref_snippet file="/home/therceman/git/gpt-tunnel-gateway/internal/service/track_lifecycle.go" lines="381-408" />
<ref_snippet file="/home/therceman/git/gpt-tunnel-gateway/internal/service/track_lifecycle.go" lines="503-520" />

The smallest correction is bounded cancellation of non-accepted active work after proving execution quiescence, preserving dispatch history and explicitly recording supersession. Reuse the existing `cancelled` outcome if suitable; do not invent fake accepted snapshots or clear dispatch history to make the Track appear planned.

If Milestone scope cannot represent this truthful supersession either, include that in the bounded lifecycle correction.

This support is separate from the RPS architecture. It is necessary for truthful lifecycle closure, not a reason to resume TSK699.

## L. Final migration/cutover order

The shortest safe path is:

1. **Repair existing candidate proof with TSK700.** It may execute before the proposed ADRs are accepted.
2. **Accept corrected decisions and explicit supersession/actor policy.**
3. **Establish Shared identity, catalog/principal foundations, and the layered harness foundation.**
4. **Prove portable Project/allocator publication and complete checkpoints, including Role state.**
5. **Migrate existing principal/grant/Session identities and activate live capability enforcement.**
6. **Cut existing active readers/writers, retirement, admission, startup, and backup scheduling to Shared/Local.** Steps 5 and 6 may overlap after prerequisites.
7. **Cut normal creation to staged Local-first bootstrap.** Old create-or-adopt entry points become unavailable, not fallbacks.
8. **Enable explicit validated restore and coordinated writable transfer using the same activation protocol.**
9. **Cut eligible execution/review routing and remove fixed-role authorization.**
10. **Complete the final Hub-authority and role-name inventories/removals.**
11. **Run owner-authorized canonical RPS recovery/onboarding.**
12. **Finish exhaustive final-model replay, receipt strengthening, and measured consolidation in parallel where safe.**

RPS treatment must be evidence-driven:

- **Abandoned bootstrap fragment:** preserve classification/provenance, prove no meaningful semantics or unresolved consumption, create fresh canonical Shared authority, and replace/quarantine the fragment only through the canonical recovery/publication path.
- **Meaningful portable semantics:** import through explicit bounded migration/restore; do not overwrite them with defaults.
- **Consumed identifiers/reservations:** obtain trustworthy allocator evidence. Maximum visible entity number alone is not proof that nothing higher was reserved.
- **Ambiguous history/high-water:** fail closed; do not substitute another code or assume counters start at one.

The known partial RPS backup must not become authority merely because its repository/code match. RPS activity derives from the new Shared activation and Local claim/binding.

Its new backup must become complete asynchronously. Publication must preserve conflict evidence and reject an unrelated lineage rather than silently overlay it.

Optional runtime attachment follows activation with RPS-scoped authority. It is not a prerequisite for declaring portable semantic onboarding complete.

## M. Pre-implementation proofs

These are proof obligations and acceptance criteria to establish before delegating the corresponding hard cuts—not a request to rerun them during this read-only review.

| Irreversible boundary | Minimum required proof |
|---|---|
| **Identity migration** | Fresh/already-migrated/conflicting/interrupted states; no code, identifier, reservation, repository, or branch-policy drift; deterministic provenance; one authority during transition. |
| **Checkpoint format** | Consistent cut across all portable families; missing/corrupt member rejection; allocator reservations/high-water preservation; same-revision conflict; Role compatibility; no runtime/credential restoration. |
| **Active Hub cutover** | Real restart with Hub unavailable and lock held; project reads, configuration, Sessions, runtimes, applicable mutation/completion, and retirement remain correct; independent backup health/retry; observed Hub-call classification. |
| **Normal bootstrap cutover** | No Hub classification; partial/complete backup does not alter creation; exact retry; every durable crash phase; staged invisibility; Local binding/claim failure; namespace conflict containment. |
| **Writable restore/transfer** | Complete pinned input; late corruption; interruption/restart; exact activation; source surrender survives restart; old-generation publication rejected; unfenced writable restore denied. |
| **Capability activation** | Reviewed old-to-new mapping; arbitrary role names; discovery/invocation agreement; grant/token/runtime impersonation denial; cross-project and Host-plane isolation; role-edit delegation effects. |
| **Live revocation** | Next-call loss of authority; queued-work denial; revocation/commit race with defined ordering; external-effect outcome reconciliation; no stale authority cache. |
| **Routing/review hard cut** | No Lead/Worker-name dependency; explicit multi-runtime assignment; disabled/revoked runtime denial; exact lease/artifact ownership; required independent reviewer cannot be impersonated via another Session. |
| **Final removals** | No reachable old authority/fixed-role fallback; independent behavioral witnesses supplement static inventories; retained strict historical/migration readers have bounded purpose. |
| **RPS production operation** | Incident-shaped disposable recovery proof; exact repository/code and consumption classification; owner authorization; deployed exact candidate; fresh readiness and affected E2E; no manual edits; complete async backup and outage usability afterward. |
| **Receipt reuse** | Trusted complete result; every material invalidation class; changed admission authority rejected; failed/cancelled/incomplete/outcome-unknown proof unusable. |
| **Test deletion/consolidation** | Named equivalent-or-stronger proof owner already passes; isolation and failure cases preserved; comparable before/after timing and flakiness evidence. |

Before coding, Lead should be able to explain each relevant obligation and its test owner. Before an irreversible integration/cutover, the corresponding proof must execute against the actual candidate.

## N. Reviewer conclusion

**YES**

After these edits, I would consider the architecture safe enough for Planner to accept the ADRs and delegate implementation.

That approval depends particularly on:

- a truthful, restricted writable-transfer guarantee;
- explicit owner bootstrap/restore claim;
- one durable activation boundary;
- live authorization with guarded commit semantics;
- complete portable Role/checkpoint coverage;
- formal ADR supersession and preserved technical-review obligations;
- proof-first cross-Track ordering.

The correction should remain bounded:

```text
one active semantic authority
+ one project authorization predicate
+ explicit, fenced writable transfer
+ one exhaustive layered contract inventory
+ uniquely valuable invariant/recovery proofs
+ exact-result reuse with fresh admission
```

The current proposals are close in direction. They are not yet precise enough to delegate unchanged.
