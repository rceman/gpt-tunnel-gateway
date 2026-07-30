# Source reuse audit

Baseline date: 2026-07-29.

| Repository | Audited commit | Used in this patch |
|---|---|---|
| `rceman/gpt-tunnel-gateway` | `97375fb57d2af5d223c5b345a4576c1ee0ec197f` | Patch base only. The one-time bootstrap finalizer and stubs are replaced. |
| `rceman/gpt-github-gateway` | `d40041417e19da8d6934757d28bda75721d34e20` | Domain concepts: versioned protocol roots, immutable tasks, supervisor/transaction discipline. No code copied because exact source files were not available inside the GPT sandbox. |
| `rceman/gpt-review-planner` | `b1a45b1e9475ab29dfd3e84d523b70897c7b8918` / VERSION `1.3.0` | Canonical GPT-authored patch-pack workflow and evidence boundary. |
| `rceman/ai-workspace` | foundation branch `86916063804ad95c3d4950ec9a843e1dc03ad914`; audited remote main `c24d3bfc4a7cc372aa8093b946e54282e22e3bbc` | Proven loopback, host-native process, PID identity, readiness, logs, and tunnel-client supervision behavior. Rust/direct-Codex design excluded. |
| `rceman/typer` | `c47dd1bcf11a11b65468008fcb024d468db1a62f` | Remote hub identity only. The gateway clones it into private managed state and uses the canonical `gpt-tunnel/v1` namespace; no user checkout is required. |

## Reuse disposition

This is a GPT-authored clean implementation using Go standard library only. It intentionally does not ask the local agent to design or author production behavior. No compatibility shims, fallback paths, adapters, or legacy protocol support are included.
