<!-- BEGIN GPT-REVIEW-PLANNER -->
> [!IMPORTANT]
> Before substantial planning, implementation, review, or correction work, load the external workflow pinned by [`.gpt-workflow.lock`](./.gpt-workflow.lock).
>
> GPT owns architecture, behavior contracts, schemas, fixtures, tests, production code, and executable patch packs. The local coding agent applies GPT-authored patches, restores dependencies, runs quality gates, and makes only narrow integration corrections proven necessary by failing gates.
<!-- END GPT-REVIEW-PLANNER -->

# Repository-specific rules

- Never expose a generic shell, arbitrary Git command, arbitrary filesystem root, or raw process execution MCP tool.
- Git exploration must use typed operations and configured projects/mirrors.
- Airelay is a bounded control channel. Never send task bodies or large prompts through `airelay prompt`.
- A task is complete only after `gpt-tunnel run finalize <run-id>` validates and publishes result/evidence to the hub.
- The GitHub hub is canonical for plans, ADRs, tasks, runs, results, evidence, and reports. Local state is disposable.
- Runtime secrets remain owner-managed outside Git.
- The active `ai-workspace` service must not be stopped or replaced without a separate cutover task and explicit owner approval.
- Use the repository-owned canonical tooling for exact-SHA CI, release publication, pinned workflow loading, and completion receipt placement. Do not replace it with direct `curl`, HTML scraping, guessed IDs, undocumented flags, or caller-selected receipt paths.
- Canonical tooling must fail closed as `BLOCKED_CANONICAL_TOOLING_GAP` when required proof is unavailable. Record rejected calls, failed calls, and bounded approved substitutions in completion evidence.
- Never extract GitHub Actions Run or job IDs with regular expressions; obtain identities only from the typed GitHub API JSON transport. This prohibition applies alongside the bans on direct `curl`, HTML scraping, inline publication verifiers, handcrafted receipt paths, and invented flags.
