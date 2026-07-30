# v0.2.1 correction review

This correction closes the blocking defects found after the v0.2.0 integration run.

## Legacy hub boundary

The daemon now fails closed when `protocol/v1`, `protocol/v2`, or `protocol/v3` data exists and `hub.allow_parallel_protocol` is false. Setting the flag to true permits isolated v4 validation only; it is not approval for production cutover. Exact compatibility adapters require importing the real legacy schemas and fixtures from `gpt-github-gateway` and the current `typer` hub.

## Cutover status

Merge and parallel testing may proceed after all gates pass. Replacing `ai-workspace` remains blocked until the legacy adapter patch and a real task → dispatch → finalize end-to-end test pass.
