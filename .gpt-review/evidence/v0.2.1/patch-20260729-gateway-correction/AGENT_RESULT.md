# v0.2.1 correction evidence

The r3 self-contained runner validated the GPT-authored transformation in two
isolated worktrees before applying it to this checkout. It printed
`GPT_TUNNEL_GATEWAY_CORRECTION_R3_READY`.

Base: `7b63d946ecd7d094268361cad0aba30d8eda69db`
Implementation: `08aac9b889f6790058c62f1358af347073b1eeda`
Evidence commit: recorded separately after this file is staged.

Gates passed twice in the runner: gofmt, go vet, go test -race, static check,
release build, all three binary checks, and git diff check. The local synthetic
MCP smoke printed `MCP_SMOKE_OK`; an additional security probe confirmed
unknown top-level arguments are rejected and ADR traversal returns a structured
error. No real Airelay session or tunnel-client was used.

No integration corrections were required. The correction is intentionally
fail-closed for legacy v1-v3 state; exact adapters and production cutover remain
blocked pending schema import and validation.

The active ai-workspace daemon and tunnel-client were not stopped, restarted,
reconfigured, replaced, deployed, or otherwise touched.
