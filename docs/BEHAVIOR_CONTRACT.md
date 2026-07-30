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
12. `run finalize` validates exact task/run identities, task hash, schema version, repository evidence, acceptance coverage, paths, commits, gates, and terminal status.
13. Failed dispatch, repository preparation failure, cancellation timeout, and result timeout produce committed terminal hub records.
14. Cancellation is cooperative and never signals the shared persistent agent process.
15. A timed-out active run is reprompted once, then terminalized if no valid finalization appears.
16. Managed Git mirror operations may fetch remote refs but never modify the project worktree or remote repository.
17. The controller verifies PID executable identity before signaling any process.
18. The patch does not stop or replace the active `ai-workspace` runtime; cutover is a separate operation.
19. Hub records exist only under the compiled canonical `gpt-tunnel/v1` namespace.
20. A gateway may execute, cancel, finalize, reprompt, or timeout only runs whose `gateway_id` equals its configured identity.
21. The controller owns `MCP_SERVER_URL` and tunnel health binding; the secret env file cannot override them.
22. Process and transaction locks are kernel-backed and recover automatically when the owning process exits.
23. The hub is addressed by repository URL and writable branch; the gateway atomically owns the only operational clone under `state_dir`, creates a missing branch from remote HEAD without force, and never requires or mutates a user hub checkout.

## Lifecycle

```text
Task state: created → dispatched → completed
                         ↘ cancelled
                         ↘ superseded

Run state: created → dispatching → awaiting_result → succeeded|failed|blocked
                                      ↘ cancel_requested → cancelled
                                      ↘ timed_out
```
