# Installation and cutover

## Safety boundary

Do not replace the running `ai-workspace` service during patch application. Build and test the Go gateway on a different port and separate state/config paths.

## Build and install

After all gates pass:

```bash
bash scripts/build-release.sh ./dist
./dist/gpt-tunnelctl install \
  --gateway-bin ./dist/gpt-tunnel-gatewayd \
  --cli-bin ./dist/gpt-tunnel \
  --ctl-bin ./dist/gpt-tunnelctl
```

Prepare `config.local.json` from `examples/config.example.json`. Set `hub.repository_url` to the private hub repository and `hub.branch` to this gateway's writable branch. Do not clone the hub repository manually and do not add it as a local project unless it is independently needed as source code. Then:

```bash
~/.local/bin/gpt-tunnelctl init-config \
  --from ./config.local.json \
  --to ~/.config/gpt-tunnel-gateway/config.json
```

On first daemon start, the gateway creates `~/.local/share/gpt-tunnel-gateway/hub/repository` (or the equivalent configured `state_dir`) and initializes the writable hub branch when necessary.

Create `~/.config/gpt-tunnel-gateway/tunnel.env` manually with mode `0600`. It must contain `CONTROL_PLANE_API_KEY` and `CONTROL_PLANE_TUNNEL_ID`. Do not place `MCP_SERVER_URL`, `MCP_COMMAND`, or `HEALTH_LISTEN_ADDR` there; the controller derives those bindings from the gateway configuration. Never put those values in a task, prompt, log, commit, or CLI argument.

## Parallel validation

Use a test MCP port such as `127.0.0.1:8875` and a separate tunnel only when available. At minimum run local smoke without touching the production tunnel:

```bash
GPT_TUNNEL_CONFIG=~/.config/gpt-tunnel-gateway/config.json \
  ~/.local/bin/gpt-tunnel-gatewayd
python3 scripts/smoke_mcp.py --url http://127.0.0.1:8875/mcp
```

Validate project Git exploration, hub read/write transactions against a test hub branch, and one end-to-end task using a test Airelay session.

## Cutover gate

For subsequent source upgrades, run `gpt-tunnelctl upgrade inspect` and resolve
every blocker before running `gpt-tunnelctl upgrade` from the exact clean
`main` checkout. A controller-independent `scripts/upgrade-bootstrap.sh`
handoff is available when the installed controller predates the upgrade
command. It verifies the release checksum manifest and invokes only the target
controller.

The upgrade lock rejects concurrent runs; the durable transaction records
backups, persisted-state operations, artifact checksums, installed/live
versions, hub revisions, PIDs, phase, and rollback availability. Binaries are
replaced atomically and restored automatically if gateway readiness, doctor, or
MCP validation fails. The tunnel-client is never restarted by this operation.

For a legacy hub, perform the owner-authorized `gpt-tunnel plan cutover` and
`gpt-tunnelctl state repair` operations first, review their exact paths and
revision guards, then rerun the read-only inspect command. Do not edit plan JSON
by hand or rewrite immutable historical runs.

Cutover requires a separate owner-approved task and these proofs:

1. all Go gates pass;
2. local MCP smoke passes;
3. hub task → plan → dispatch → finalize passes;
4. Git mirror and worktree tools return correct refs/diffs;
5. gateway-only restart passes;
6. existing tunnel configuration is backed up without exposing secrets;
7. rollback commands are documented and tested.

Only then stop old `workspace-agentd`/tunnel-client and start `gpt-tunnelctl`. If ChatGPT connector verification fails, stop the new pair and restore the old controller immediately.
