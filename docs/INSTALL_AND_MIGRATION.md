# Installation and migration

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

Prepare `config.local.json` from `examples/config.example.json`, then:

```bash
~/.local/bin/gpt-tunnelctl init-config \
  --from ./config.local.json \
  --to ~/.config/gpt-tunnel-gateway/config.json
```

Create `~/.config/gpt-tunnel-gateway/tunnel.env` manually with mode `0600`, preserving the existing owner-managed values. Never put those values in a task, prompt, log, commit, or CLI argument.

## Parallel validation

Use a test MCP port such as `127.0.0.1:8875` and a separate tunnel only when available. At minimum run local smoke without touching the production tunnel:

```bash
GPT_TUNNEL_CONFIG=~/.config/gpt-tunnel-gateway/config.json \
  ~/.local/bin/gpt-tunnel-gatewayd
python3 scripts/smoke_mcp.py --url http://127.0.0.1:8875/mcp
```

Validate project Git exploration, hub read/write transactions against a test hub branch, and one end-to-end task using a test Airelay session.

## Cutover gate

Cutover requires a separate owner-approved task and these proofs:

1. all Go gates pass;
2. local MCP smoke passes;
3. hub task → plan → dispatch → finalize passes;
4. Git mirror and worktree tools return correct refs/diffs;
5. gateway-only restart passes;
6. existing tunnel configuration is backed up without exposing secrets;
7. rollback commands are documented and tested.

Only then stop old `workspace-agentd`/tunnel-client and start `gpt-tunnelctl`. If ChatGPT connector verification fails, stop the new pair and restore the old controller immediately.
