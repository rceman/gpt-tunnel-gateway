# Test gate separation

The repository has distinct verification purposes:

1. **Worker correctness:** focused or affected deterministic tests plus the
   cache-aware `scripts/test-fast.py` runner. This lane does not use
   `-count=1`; its default committed-diff base is `origin/main` (then `main`),
   with `GPT_TEST_BASE` and `--base` overrides.
2. **Canonical task correctness:** `task/test` invokes the project-owned
   `scripts/test-full.sh` runner once for the exact reviewed candidate. The
   runner discovers every package, shards the large service and MCP packages
   into bounded test-name groups, and invokes every discovered test with
   `-count=1`; it does not omit coverage or use Go's test-result cache. It
   exercises deterministic code/logic correctness only: `livee2e`- and
   `liveperformance`-tagged workloads are outside the default Go build and are
   never invoked transitively.
3. **Live E2E:** `scripts/test-e2e.sh` runs the `livee2e`-tagged candidate
   restart/debug-activation tests, which require `GTW_CANDIDATE_*` environment
   variables and skip when unset. The deterministic Python `integration_activate`
   unit suite remains a separate script lane via
   `scripts/test-integration-activate.sh`.
4. **Live performance:** `scripts/test-performance.py` runs the tagged
   representative local-code inspection scenario twice with a shared build
   cache, reports cold and warm timings, and records environment identity.
5. **Timing/profile and race:** `scripts/test-profile.py` reports package/test
   durations and top slow contributors. `scripts/test-race.sh` runs the same
   deterministic corpus under the Go race detector. Both remain explicit lanes
   and are never part of normal full correctness.

Worker does not run the canonical full runner before `submit-code`; the code
candidate carries production and focused test changes together. Task verification
owns one full run for a materially unchanged exact candidate and reuses a valid
successful receipt only when the Task revision, accepted reviews, base,
candidate head/tree, branch, and gate profile all still match.

## Temporary script-level lane mapping

The lane commands above are a script-level mapping: TSK602/TSK603 (tracked
with TSK559) will absorb the same semantics into project-owned quality
profiles. The mapping lives only in `scripts/test-full.py` (lane selection and
this inventory), the thin lane scripts, and this document, so the future
project-owned profiles can adopt it without duplicating policy.

## Gate20 inventory and classification

The inventory covered every Go `*_test.go` file and searched for wall-clock
measurements, duration comparisons, benchmark declarations, sleeps, polling
deadlines, timeout contexts, and performance/latency names. Every candidate
was classified before the Worker policy changed.

### Machine-sensitive pass/fail checks removed from routine correctness

These checks asserted initiation or request duration and could fail because of
host load, package concurrency, cache state, or scheduling. They now retain
functional receipt, idempotency, and terminal-state assertions without timing
thresholds:

- `internal/service/task_execution_*_test.go` — canonical Task execution dispatch, review, integrate.
- `internal/service/task_supersede_async_test.go` — task supersede.
- `internal/service/task_execution_async_test.go` — task work/finalize.
- `internal/service/task_create_async_test.go` — task create receipt.
- `internal/service/task_authoring_update_async_test.go` — task update receipt.
- `internal/service/task_authoring_ready_async_test.go` — task ready receipt.
- `internal/service/project_configuration_async_tests_project_config_test.go` —
  project update receipt.
- `internal/service/agent_ipc_async_test.go` — agent prompt initiation.
- `internal/service/adr_create_async_test.go` — ADR create initiation.
- `internal/service/liveness_progress_test.go` — project-status wall-clock
  threshold.
- `internal/mcp/gateway_status_shared_test.go` — gateway-status wall-clock
  threshold.
- `internal/mcp/agent_local_authority_test.go` — agent/await wall-clock
  threshold.
- `internal/mcp/generic_system_await_test.go` — cancellation wall-clock
  threshold.
- `internal/mcp/generic_agent_actions_tests_agent_wait_test.go` — await
  elapsed thresholds.
- `internal/releaseartifacts/releaseartifacts_test.go` — subprocess deadline
  grace-period threshold.
- `internal/airelay/client_tests_status_fixture_test.go` — prompt deadline
  grace-period threshold.
- `internal/activation/activate_test.go` — MCP smoke deadline grace-period
  threshold.
- `internal/mcp/code_public_e2e_tests_fixture_read_test.go` — one-second
  per-call performance threshold. Token ceilings, pagination, heads, and
  functional output checks remain.

### Moved to the explicit live performance lane

`internal/service/local_code_inspection_perf_test.go` was the material
performance gate. It is now tagged `liveperformance`, renamed
`TestLocalCodeInspectionPerformanceProfile`, and excluded from ordinary
`go test ./...`. It still exercises representative repository-backed
`code/worktree`, `code/tree`, `code/search`, `code/read`, and `code/diff`
operations and reports each measured operation. The tagged
`internal/mcp/public_code_performance_test.go` preserves the representative
public `code/search` latency check outside routine correctness. The explicit
runner records cold/warm process timings and environment identity and enforces
the warm 10-second target outside routine correctness.

### Moved to the explicit live E2E lane

`cmd/gpt-tunnel-gatewayd/runtime_restart_candidate_e2e*_test.go` requires a
built candidate binary through `GTW_CANDIDATE_GATEWAY_BINARY`,
`GTW_CANDIDATE_SOURCE_SHA`, and `GTW_CANDIDATE_SOURCE_ROOT`, so its two tests
(`TestCandidateGatewayRestartMCPNetworkE2E`,
`TestCandidateDebugActivateMCPNetworkE2E`) ran only as env-gated skips in the
normal corpus. The whole candidate-E2E file group is now behind the `livee2e`
build tag and runs through `scripts/test-e2e.sh`; assertions are unchanged.

Classification is by semantics, not name substring. Tests whose names contain
"E2E" but that exercise only in-process deterministic boundaries (`httptest`
servers, temporary repositories, stub binaries, helper-process re-exec) remain
in the full deterministic corpus. Only genuinely live/environment-sensitive
workloads move to explicit lanes.

### Converted to deterministic functional coverage

- `internal/service/local_code_inspection_latency_test.go` no longer samples
  or reports latency. It verifies clean-main search resolution, pagination,
  zero-match behavior, continuation behavior, and the absence of avoidable Git
  fetch/worktree operations.
- The public code E2E fixture keeps token, pagination, exact-head, diff, and
  continuation assertions but no longer fails on response duration.
- Async tests retain bounded polling deadlines and short polling sleeps only as
  termination protection for asynchronous state transitions. Those are not
  performance claims and do not assert how quickly a correct operation must
  complete.

### Deliberately retained non-gates

The following are not machine-performance acceptance checks:

- `BenchmarkSharedAndLocalTraffic` is a Go benchmark and runs only when a
  benchmark command is explicitly requested.
- Fixture timestamps and revision-ordering sleeps create deterministic ordering
  data or distinguish durable records; they do not assert elapsed host time.
- `internal/gates/gates_tests_gate_test.go` uses synthetic `DurationMS` values
  to test warning classification, not a measured test runtime.
- Context deadlines, process-readiness polling, operation polling, and
  cancellation tests remain bounded liveness safeguards. Their functional
  assertion is the returned error/state, not a throughput or SLO threshold.

After changes, a repeat search must find no unexplained machine-load-sensitive
pass/fail timing gate in the normal correctness corpus. Remaining time calls
must be one of the retained classifications above or belong to the explicit
performance/profile lanes.

### Post-cutover inventory rerun

The post-cutover search was rerun across `internal/**/*_test.go` for elapsed
comparisons, duration/latency/SLO terms, benchmark declarations, sleeps,
deadlines, and timeout contexts. The only elapsed pass/fail threshold remains
under the `liveperformance` build tag. Normal-corpus matches are bounded
polling, cancellation/readiness protection, deterministic record-ordering
fixtures, output-size/token ceilings, or synthetic gate-warning inputs; the
only benchmark is explicit `BenchmarkSharedAndLocalTraffic`. No unexplained
machine-load-sensitive timing gate remains in ordinary correctness execution.
