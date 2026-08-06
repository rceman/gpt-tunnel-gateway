# gpt-tunnel-gateway

GitHub-backed MCP gateway for persistent ChatGPT-to-local-agent workflows, project planning, task orchestration, read-only Git exploration, and Airelay dispatch.

The project replaces the Rust `workspace-agentd` runtime after an explicit, verified cutover. It keeps the existing OpenAI `tunnel-client` and owner-managed tunnel credentials.

## Components

- `gpt-tunnel-gatewayd`: loopback-only Streamable HTTP MCP daemon.
- `gpt-tunnel`: typed project, plan, ADR, task, run, and Git CLI.
- `gpt-tunnelctl`: host-native install, lifecycle, health, and log controller.

Inspect and upgrade an installed runtime only from a clean, synchronized
`main` checkout:

```bash
gpt-tunnelctl upgrade inspect
gpt-tunnelctl upgrade status
gpt-tunnelctl upgrade
```

`upgrade inspect` reports the complete target state graph before activation.
The upgrade command records a durable transaction under the configured state
directory, validates the exact release artifact set, locks concurrent upgrades,
atomically replaces all three binaries, restarts only the gateway, verifies
MCP/readiness invariants, and rolls back all binaries on post-install failure.
It never restarts the tunnel-client or changes config/secrets.

For a previous controller that does not know the upgrade command, use the
verified release bundle entry point:

```bash
bash scripts/upgrade-bootstrap.sh <verified-release-directory> <clean-main-root>
```

The bootstrap verifies `SHA256SUMS` before handing off to the target controller.

## Canonical workflow

```text
task create
→ plan update / plan section-create
→ task dispatch
→ airelay prompt <session> "Read task and execute it. Run: gpt-tunnel task read <task-id>"
→ agent writes one strict completion.json
→ gpt-tunnel run finalize <run-id>
→ the gateway derives repository proof and commits one canonical report to the GitHub hub
```

A successful Airelay delivery is non-terminal. Completion exists only after hub finalization succeeds.

## Canonical identifiers

New operational records use compact project-coded identifiers with a positive
safe-integer counter:

- tasks: `CODE-TSK<N>`;
- runs: `CODE-TSK<N>-RUN<M>`;
- ADRs: `CODE-ADR<N>`;
- operator journal events and corrections: `CODE-OPR<N>`.

Each adopted project owns separate task, ADR, run, and operator counters. A
task-create input must include `project_id`, `slug`, `title`, and `objective`;
the gateway derives the task branch as `task/<task-id>-<slug>` and resolves
`base_revision` from the authoritative remote default-branch head. Callers do
not provide `branch` or `base_revision`, and allocator conflicts are retried
only for unpinned writes within a bounded limit; pinned writes fail fast.

Pre-cutover workflow-v1 task, run, ADR, and operator identifiers remain
readable as immutable history where supported. They are never selected as
current execution state and are rejected by operational mutation paths.

Plans use a schema-v2 compact manifest. Use `gpt-tunnel plan read` for the bounded manifest, `plan section-read` for one full section, and `plan render` only when a complete human-readable composition is required. Manifest updates are partial; section updates use independent optimistic revisions.

For a stalled active run, `gpt-tunnel run agent-tail <run-id> [--lines N]` reads a bounded, read-only tail from the run's stored Airelay session; it never accepts a caller-supplied session key or skip option.

For a short follow-up to a registered project agent, use the separate v0.6.0
direct-session surface:

```text
gpt-tunnel agent status <project-id>
gpt-tunnel agent tail <project-id> --lines 4 --skip 0
gpt-tunnel agent send <project-id> --text '<short message>'
```

The gateway derives the Airelay session key from project configuration, bounds
messages and output, serializes sends, and returns exact delivery/status data.
These calls do not create tasks or runs and do not mutate plans or Git; use the
durable workflow for authorized implementation work.

For a bounded structural review, use `gpt-tunnel run review-snapshot <run-id>`. It refreshes the managed mirror once and returns deterministic task, artifact, repository, and invariant-check data without dispatching work or exposing session details or local paths.

Before activation or recovery, use `gpt-tunnelctl state check` and review
`gpt-tunnelctl state repair --dry-run`. The apply form creates a hub backup and
performs only the canonical mutable-state repair transaction.

## Development gates

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
python3 scripts/static-check.py
bash scripts/build-release.sh
git diff --check
python3 scripts/upgrade_rehearsal.py
```

Canonical proof helpers are documented in
[`docs/CANONICAL_AGENT_TOOLING.md`](docs/CANONICAL_AGENT_TOOLING.md). Use the
exact-SHA CI checker, release-publication verifier, pinned-workflow loader and
derived-path completion receipt writer rather than ad hoc GitHub/API or
handcrafted receipt commands.

GPT authors source and tests but does not execute runtime gates in the review-planner workflow. The local agent runs these commands after applying the patch pack.

## Configuration

Copy `examples/config.example.json`, replace machine-specific paths and project mappings, then install it without overwriting an existing config:

```bash
gpt-tunnelctl init-config \
  --from ./config.local.json \
  --to ~/.config/gpt-tunnel-gateway/config.json
```

The hub configuration contains a Git repository URL and writable branch, not a local checkout path. The gateway creates and owns its managed hub clone under `state_dir/hub/repository`; `rceman/typer` does not need to exist as a user project checkout.

Secrets are not stored in the JSON config. Preserve the existing owner-managed tunnel environment file separately with mode `0600`.

See `docs/INSTALL_AND_CUTOVER.md` before any runtime cutover.
