# Behavior contract

## Core invariants

1. The daemon binds only to a configured loopback address and rejects non-loopback Host, Origin, and remote callers.
2. No MCP tool accepts arbitrary executable names, argument vectors, Git commands, shell strings, environment maps, or filesystem roots.
3. Projects are addressed only by configured IDs.
4. Hub writes require an optional optimistic revision and always use a non-force push followed by remote verification.
5. Task creation, plan update, and task dispatch are separate explicit operations.
6. Immutable task content is never edited. Cancellation and supersession update a separate task-state record.
7. Dispatch requires the global plan to identify the task as active.
8. The project worktree must be clean and the exact base commit must resolve before branch preparation.
9. An existing task branch is accepted only when the task base is an ancestor; otherwise dispatch fails without reset, clean, delete, or force-update.
10. Airelay messages are at most 256 bytes and contain no task body.
11. Successful prompt delivery moves a run to `awaiting_result`; it never means success.
12. The agent order is commit, run every required gate, push the task branch, write the gateway-owned `completion.json`, then finalize. `run finalize` accepts only a task branch published at the final local HEAD; synthetic terminalization uses the refreshed published task branch when valid, falls back to the immutable base only when that branch is absent, and rejects invalid published proof.
13. Failed dispatch, repository preparation failure, cancellation timeout, and completion timeout produce one canonical failed report; they never create result or evidence artifacts.
14. Cancellation is cooperative and never signals the shared persistent agent process.
15. The canonical plan is schema-v2: a compact manifest plus independently versioned named sections; no full section description is returned by `plan_read` or compact project status.
16. Manifest updates are partial and preserve omitted fields. Section updates and deletes require an independent optimistic section revision; unrelated sections do not conflict.
17. A current schema-v1 monolithic plan is converted only by the explicit owner-invoked `plan_cutover` operation. It decomposes headings, objective, and queue items into schema-v2 records, proves every meaningful source line is represented, and then removes the legacy body; ordinary reads never migrate and there is no v1 fallback, alias, or dual write.
18. A timed-out active run is reprompted once, then terminalized as failed if no valid finalization appears.
19. Managed Git mirror operations may fetch remote refs but never modify the project worktree or remote repository.
20. The controller verifies PID executable identity before signaling any process.
21. The patch does not stop or replace the active `ai-workspace` runtime; cutover is a separate operation.
22. Hub records exist only under the compiled canonical `gpt-tunnel/v1` namespace.
23. A gateway may execute, cancel, finalize, reprompt, or timeout only runs whose `gateway_id` equals its configured identity.
24. The controller owns `MCP_SERVER_URL` and tunnel health binding; the secret env file cannot override them.
25. Process and transaction locks are kernel-backed and recover automatically when the owning process exits.
26. The hub is addressed by repository URL and writable branch; the gateway atomically owns the only operational clone under `state_dir`, creates a missing branch from remote HEAD without force, and never requires or mutates a user hub checkout.
27. Every MCP tool declares an exact output schema and all four behavioral hints; successful structured output is validated before transmission, while tool failures omit `structuredContent`.
28. `tools/call.params` accepts only `name`, `arguments`, and an optional bounded protocol `_meta` object; all other envelope fields and unknown tool arguments are rejected.
29. `gpt-tunnelctl upgrade` is source-only and requires clean synchronized `main`; it performs a locked transactional three-binary replacement, gateway-only restart, native doctor/MCP validation, and automatic all-binary rollback on failure.
30. Fresh managed-hub startup fails readiness when configured project IDs lack durable canonical project records; it never silently reports a ready but empty project bus.
31. `run_review_snapshot` is a bounded read-only aggregate: it refreshes the managed mirror once, omits session and local-path details, and reports deterministic structural checks for active or terminal runs without dispatching work.
32. New runs expose only one local `completion.json`; new hub run state is exactly `run.json` plus canonical `report.json`.
33. Terminal completion statuses are only `succeeded`, `failed`, and `needs_gpt_revision`; every terminal path clears active task/run ownership. Historical protocol-v1 run records are bounded, read-only projections with legacy paths redacted and are rejected by every operational run path.
34. `run_report` refreshes the managed mirror before validating canonical Git proof. Report proof uses only mirror-resolved HEAD, ancestry, bounded oldest-to-newest commits, canonical changed files, and task/default-branch reachability; an exact immutable task base remains valid when its task branch is absent, while other absent-branch heads must remain reachable from the default branch. It never requires the checked-out project worktree to contain the task commit.
35. `gpt-tunnelctl upgrade inspect` reports the complete target-runtime blocker set in one pass; readiness timeout is never the only startup diagnostic.
36. Installed and running gateway versions are independently observable. A successful upgrade requires `version_match`, a new gateway PID, and the unchanged tunnel PID.
37. Upgrade transaction state is durable under the configured state directory; a temporary release directory cannot be the sole authority for rollback or completion.
38. Project registration atomically creates a valid idle workflow-v2 plan, so startup cannot observe a configured active project without its current plan.
39. The MCP descriptor contract is driven by one canonical tool manifest. Tool counts are derived from registration/manifest parity; smoke tests require names and schema/handler parity rather than a stale numeric assertion.

## Lifecycle

```text
Task state: created → dispatched → completed
                         ↘ ready (failed or needs_gpt_revision; recoverable)
                         ↘ cancelled
                         ↘ superseded

Run state: created → dispatching → awaiting_result → succeeded|failed|needs_gpt_revision
                                      ↘ cancel_requested → failed
```
