# Security

## Trust boundaries

- ChatGPT/tunnel-client to loopback MCP daemon.
- MCP daemon to configured GitHub hub clone.
- MCP daemon to configured project worktrees and mirrors.
- MCP daemon to owner-managed Airelay executable.
- Controller to gateway and tunnel-client processes.

## Controls

- loopback bind and caller validation;
- strict JSON decoding and bounded request body;
- allowlisted projects and configured roots;
- typed Git operations with revision/path validation;
- Git config isolation, pager/external diff disabling, non-interactive authentication;
- no shell invocation for Git or Airelay;
- bounded output, list, message, completion, and report sizes;
- immutable task hashing;
- optimistic hub concurrency and plain push;
- exact remote commit verification;
- atomic file writes with restrictive modes;
- executable identity checks before stop/restart;
- owner-managed secrets outside Git and prompts.

Historical protocol-v1 run records are decoded through a bounded strict read
model. Legacy local result/evidence paths are never returned publicly, and
legacy result/evidence files are never accepted as workflow-2.0 completion.

## Explicitly forbidden

- generic command execution;
- `git push --force` or history rewriting;
- arbitrary project registration by untrusted remote path;
- direct `codex exec` or ephemeral Codex spawning;
- sending complete tasks through Airelay;
- logging tunnel API keys or private credentials.
