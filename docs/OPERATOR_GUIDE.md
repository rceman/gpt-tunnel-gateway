# Operator guide

The gateway owns the managed hub clone under `state_dir/hub/repository` and
the configured authoritative hub branch. Verify both the local checkout HEAD
and the remote branch SHA; they are intentionally separate.

Routine checks:

```text
gpt-tunnelctl status
gpt-tunnelctl doctor
gpt-tunnelctl upgrade inspect
gpt-tunnelctl upgrade status
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

## Explicit Task Trains

An authorized train is an explicit, ordered list of Task IDs persisted by the
gateway. It never discovers or appends backlog work. Create one from a bounded
input file, then use its status and poll operations:

```text
gpt-tunnel task train-create --file <train-input.json>
gpt-tunnel task train-status <project-id>
gpt-tunnel task train-poll <project-id>
```

The watcher polls every 20 seconds. For an active run, each poll obtains one
bounded current viewport through `gpt-tunnel run agent-tail <run-id> --lines 10`.
It does not reread the same viewport, send duplicate prompts, merge branches,
or mark Delivery review complete. A finalized task waits for Delivery; only a
durably merged task permits the next explicitly listed task to be dispatched.
Failed, blocked, rejected, cancelled, or deferred tasks stop the train. After
the final listed task is merged, the train becomes completed and idle.

## Release lifecycle

Gateway v0.6.1 tooling adoption is Stage A `implementation_unreleased`:

```text
python3 scripts/validate-release-tool-conformance.py --release-script scripts/release.py --ci-script scripts/check-github-ci.py
python3 scripts/release.py check-source
python3 scripts/release.py check
python3 scripts/check-github-ci.py --repository rceman/gpt-tunnel-gateway --sha-from-git HEAD --policy required --wait --format json
```

These checks do not publish a release. A separate owner-authorized
`release_publication` task must perform prepare, release readiness, the
release-only commit, exact-SHA CI, annotated tagging, and tag verification.
Never manually edit VERSION, synchronized version files, or dated changelog
headings, and never infer publication from an implementation check.
