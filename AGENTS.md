# Repository rules

- Never expose a generic shell, arbitrary Git command, arbitrary filesystem root, or raw process execution MCP tool.
- Git exploration must use typed operations and configured projects or mirrors.
- Airelay is a bounded control channel; never send task bodies or large prompts through it.
- Use the current canonical GTW MCP/actions/guides, Tasks, ADRs, Rules, and repository-local project declarations for authority.
- Keep runtime secrets owner-managed outside Git.
- Do not stop or replace an active managed service without explicit owner approval and a bounded cutover task.
- Use repository-owned canonical tooling for exact-SHA CI, release publication, and completion evidence; do not replace it with direct curl, HTML scraping, guessed IDs, undocumented flags, or caller-selected receipt paths.
- Canonical tooling must fail closed when required proof is unavailable; record rejected calls, failed calls, and approved bounded substitutions in completion evidence.
- Never extract GitHub Actions Run or job IDs with regular expressions; obtain them through typed GitHub API JSON transport.
