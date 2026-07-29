# Go gateway patch evidence

The canonical source archive checksum matched the relocatable manifest entry.
The repack-2 apply script verified the source-only tree
`96c56e6a7258629d11fb805a6c0ebb3fa2186aea` and printed
`GPT_SOURCE_TREE_APPLIED`.

## Commits

- Implementation: `740fe7ff93f5171362a5ed7526e95cdb812c1849`
- Integration corrections and generated-artifact cleanup: `e6dee889bbc94044d9a85570cb4c706d33ac6a8c`

## Gates

`gofmt`, `go vet ./...`, `go test -race ./...`, `python3 scripts/static-check.py`,
`bash scripts/build-release.sh`, all three binary builds, and diff checks passed.
The local synthetic-hub MCP smoke test printed `MCP_SMOKE_OK` on port 18875.
No real Airelay session was contacted and no real tunnel-client was started.

## Corrections

Two source defects were corrected narrowly: missing reprompt fields in `model.Run`,
and the invalid empty `GIT_WORK_TREE` environment variable for mirror commands.
The release-generated `dist/` output was removed in a separate cleanup commit.

The active `ai-workspace` daemon and tunnel-client were not stopped, restarted,
reconfigured, deployed, or otherwise touched.
