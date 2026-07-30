# Architecture

## System boundary

```text
ChatGPT
  ↓ Secure MCP Tunnel
existing tunnel-client
  ↓ loopback Streamable HTTP
 gpt-tunnel-gatewayd (Go)
  ├── typed MCP API
  ├── GitHub hub transactions
  ├── managed read-only Git mirrors
  ├── local worktree inspection
  ├── project/plan/ADR/task/run service
  ├── Airelay bounded dispatch
  └── result/evidence validation
```

`gpt-tunnel-gatewayd` is a control plane, not a remote shell. Every operation maps to a bounded typed method.

## Durable and local state

GitHub hub state is canonical and cross-device:

- projects;
- global plans and history;
- accepted/superseding ADRs;
- immutable hashed tasks and mutable task-state records;
- run lifecycle;
- results, evidence, and deterministic reports.

Local state is machine-specific and disposable:

- configured project roots;
- the gateway-managed hub clone under `state_dir/hub/repository`;
- managed read-only project Git mirrors;
- gateway and session mapping;
- run result staging paths;
- kernel-backed locks, PID files, and logs.

## Hub transaction

The hub config declares only `repository_url` and the writable branch. On startup, the gateway atomically creates its own clone under `state_dir/hub/repository`, verifies the exact `origin` URL, fetches refs, and creates the configured branch from the remote default branch only when that branch does not exist. Existing branches are never reset or force-updated.

Every write uses a dedicated detached temporary Git worktree:

```text
ensure managed clone and configured branch
→ fetch origin
→ resolve exact remote branch revision
→ compare expected_hub_revision
→ create detached temporary worktree
→ atomically write validated files
→ deterministic commit identity/message
→ plain non-force push
→ ls-remote verification
→ remove temporary worktree
```

The managed hub checkout is not dirtied or switched, and no user checkout of the hub repository is required.

## Git exploration

Committed history is read from managed bare mirrors so refreshes do not race with the agent's worktree. Local staged/unstaged state is exposed through separate typed worktree tools.

## Agent transport

Airelay carries only a short action and task-reading command. The complete execution packet is generated locally by `gpt-tunnel task read <task-id>`. The persistent session is owner-managed; the gateway never starts or resumes Codex.

## Runtime binding

The controller starts `tunnel-client` with a canonical `MCP_SERVER_URL` derived from the configured loopback gateway address and a configured loopback health listen address. The owner-managed mode-0600 env file contains only control-plane credentials and explicitly allowed tunnel options.
