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

## Test gate separation

Routine Worker verification is the focused or affected deterministic set plus the
cache-aware fast runner:

```text
python3 scripts/test-fast.py --affected
```

The fast lane deliberately does not pass `-count=1`; it uses Go's native cache
and expands changed Go packages to their repository-local dependents. By
default, committed changes are compared with `origin/main` (then `main`); set
`GPT_TEST_BASE` or pass `--base <ref>` when the assigned base is different.
Working-tree and staged changes are always included. An unknown-impact change
fails safe to all packages. Documentation-only changes have no Go package to
execute.

`task/test` owns the complete deterministic correctness proof once for the exact
candidate. Its project-owned test gate is the canonical uncached runner. It
discovers every package and shards the large service and MCP packages into
explicit test-name groups; no deterministic test coverage is omitted. It runs
deterministic code/logic correctness only — `livee2e`- and
`liveperformance`-tagged workloads are outside its build:

```text
./scripts/test-full.sh
```

Worker never runs that full runner before `submit-code`; the code candidate
carries production and focused test changes together. Specialist workloads are
separate explicit lanes and are never invoked transitively by full: live
candidate E2E via `scripts/test-e2e.sh` (`livee2e` tag, `GTW_CANDIDATE_*`
environment), live performance via `scripts/test-performance.py --output
<performance-report.json>`, uncached timing/profile evidence via `python3
scripts/test-profile.py --output <profile-report.json>`, and race detection
over the same deterministic corpus via `scripts/test-race.sh`. The full runner
is also the only default Task verification command. When the Task revision,
accepted reviews, base, candidate head/tree, branch, and gate profile are
unchanged, `task/test` reuses the authoritative successful verification receipt
instead of rerunning it.

## Durable server operations

Asynchronous mutation responses return a compact project-scoped Operation key,
such as `GTW-OPR1`. Use `operation/read` for a bounded durable projection and
`operation/await` for a read-only wait; the latter defaults to 30 seconds and
is capped at 60 seconds. These actions never retry or replay the mutation.
`agent/await` remains exclusively managed-Agent runtime supervision. Do not
wait for Task or server mutations with `agent/await`, shell sleep, or process
inspection.

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
