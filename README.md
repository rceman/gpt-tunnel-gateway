# gpt-tunnel-gateway

GitHub-backed MCP gateway for persistent ChatGPT-to-local-agent workflows, project planning, task orchestration, read-only Git exploration, and Airelay dispatch.

The project replaces the Rust `workspace-agentd` runtime after an explicit, verified cutover. It keeps the existing OpenAI `tunnel-client` and owner-managed tunnel credentials.

## Components

- `gpt-tunnel-gatewayd`: loopback-only Streamable HTTP MCP daemon.
- `gpt-tunnel`: typed project, plan, ADR, task, run, and Git CLI.
- `gpt-tunnelctl`: host-native install, lifecycle, health, and log controller.

## Canonical workflow

```text
task create
→ plan update
→ task dispatch
→ airelay prompt <session> "Read task and execute it. Run: gpt-tunnel task read <task-id>"
→ agent writes agent-result.json + evidence.json
→ gpt-tunnel run finalize <run-id>
→ result/evidence/report committed and pushed to the GitHub hub
```

A successful Airelay delivery is non-terminal. Completion exists only after hub finalization succeeds.

## Development gates

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
python3 scripts/static-check.py
bash scripts/build-release.sh
git diff --check
```

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
