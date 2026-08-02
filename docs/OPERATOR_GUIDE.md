# Operator guide

The gateway owns the managed hub clone under `state_dir/hub/repository` and
the configured authoritative hub branch. Verify both the local checkout HEAD
and the remote branch SHA; they are intentionally separate.

Routine checks:

```text
gpt-tunnelctl status
gpt-tunnelctl doctor
gpt-tunnelctl upgrade inspect
gpt-tunnelctl state check
gpt-tunnelctl diagnose-startup
```

Never print tunnel environment files or API keys. Gateway-only operations must
preserve the tunnel PID. Do not stop the old runtime or activate a new one
until target preflight and rollback preparation pass.

For an upgrade, record the authoritative hub branch SHA, config checksum,
installed/live versions, gateway PID, and tunnel PID before activation. A
successful transaction must expose a new gateway PID and the same tunnel PID;
if activation fails, use the durable rollback result and enter diagnosis-only
mode after the second failed activation.

For a short follow-up to a registered project agent, use the direct session
surface:

```text
gpt-tunnel agent status <project-id>
gpt-tunnel agent tail <project-id> --lines 4 --skip 0
gpt-tunnel agent send <project-id> --text '<short message>'
```

The send is serialized and returns a delivery receipt. It does not create a
task or run and must not be used as a substitute for an authorized durable
workflow. It is emergency/control-plane communication only: it cannot create
new scope, authorize implementation, approve a merge or release, or authorize
deployment. For example, do not use it to send “implement the next feature”,
“merge and release this branch”, “deploy this”, or “continue the roadmap”.

Use the aggregated progress snapshot for routine checks:

```text
gpt-tunnel project status <project-id>
```

For an active run classified as `compacted_idle`, use exactly one:

```text
gpt-tunnel run resume <run-id>
```

The gateway generates the recovery instruction and records bounded operational
events. Do not send a bare `continue`; do not retry a resume after
`STALLED_AFTER_COMPACTION` without explicit review. A low-context warning is
not compaction evidence.
