# Architecture

`gpt-tunnel` is a typed CLI over a GitHub-backed hub. `gpt-tunnel-gatewayd`
serves loopback-only Streamable HTTP MCP with strict Host/Origin validation;
`gpt-tunnelctl` manages the host-native `tunnel-client` lifecycle. Hub writes
follow fetch → revision check → validation → atomic update → deterministic
commit → push → remote verification.

The first slice reserves versioned paths for projects, plans, ADRs, tasks, runs,
results, evidence, and reports. Tasks and accepted ADRs are immutable;
supersession creates a new object. Airelay receives only:
`Read task and execute it. Run: gpt-tunnel task read <task-id>`.
