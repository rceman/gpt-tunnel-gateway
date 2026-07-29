# Source reuse audit

Audit date: 2026-07-29. All source repositories were cloned/read-only; none
were modified.

| Repository | Remote main SHA | Relevant material | Decision |
|---|---|---|---|
| `rceman/gpt-github-gateway` | `d40041417e19da8d6934757d28bda75721d34e20` | `protocol/v1`, `protocol/v2`, `protocol/v3`; `internal/task`; supervisor and transactional patterns | Reuse concepts/contracts selectively; Go implementation remains independently versioned. |
| `rceman/gpt-review-planner` | `b1a45b1e9475ab29dfd3e84d523b70897c7b8918` | executable patch-pack templates, evidence workflow, VERSION `1.3.0` | Canonical workflow pinned in `.gpt-workflow.lock`; no code copied without compatibility review. |
| `rceman/ai-workspace` | `c24d3bfc4a7cc372aa8093b946e54282e22e3bbc` | host controller behavior documented in README and runtime implementation | Port behavior to Go later; never modify or control its active daemon here. |
| `rceman/typer` | `c47dd1bcf11a11b65468008fcb024d468db1a62f` | current cross-device hub | Preserve compatible hub paths and schemas; credentials were not read. |

Exclusions: Rust runtime, direct Codex spawning, generic shell/filesystem MCP,
secrets, and changes to source repositories. The hub is canonical; local files
are limited to config, locks, caches, logs, and temporary artifacts.
