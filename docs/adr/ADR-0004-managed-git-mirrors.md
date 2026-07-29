# ADR-0004: Explore committed Git through managed mirrors

Status: accepted.

The gateway refreshes configured bare mirrors and reads all refs without switching or fetching into an agent's active worktree. Local uncommitted state uses separate worktree tools.
