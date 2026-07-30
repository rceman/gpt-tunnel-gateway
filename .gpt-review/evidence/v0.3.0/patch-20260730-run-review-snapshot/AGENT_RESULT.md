# Run review snapshot evidence

The implementation adds the bounded `run_review_snapshot` MCP aggregate and
the matching `gpt-tunnel run review-snapshot <run-id>` CLI operation. It reads
durable task/run artifacts, refreshes the configured managed mirror once, and
returns deterministic structural review data without dispatching work or
exposing session or local-path details.

Base: `86c269a6892e584e67a13357cdf8a265a09af208`
Implementation: `5f7a1f53bb3d828b5023e6476f9f0dd186a16278`
Evidence commit: recorded separately after this file is staged.

The focused active-run service test and MCP schema/annotation/output-contract
tests passed. The complete repository gates passed, including formatting, vet,
race tests, static checks, relocatable release checksum verification, all three
0.3.0 binary version checks, and `git diff --check`.

No runtime configuration, tunnel environment, installed binary, service,
connector, hub migration, release, or deployment was touched.
