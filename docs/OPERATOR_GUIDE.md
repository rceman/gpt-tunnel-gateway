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
workflow.
