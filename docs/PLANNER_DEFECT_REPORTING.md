# Planner defect reporting

Use this bounded protocol when a Gateway action or canonical lifecycle gate
exposes a defect:

1. Preserve the exact durable identity: project, Train/Task/Attempt when
   applicable, operation ID, source or checkpoint SHA, and the action name.
2. Read the existing receipt or report once. Do not re-submit a terminal
   operation, manufacture completion evidence, or overwrite immutable evidence.
3. Report the terminal status and exact error or failing gate, with the
   smallest relevant file/function and reproducible input. Keep logs and
   outputs bounded; redact request contents and secrets.
4. Distinguish a code defect from an unavailable dependency, stale state, or
   missing canonical tooling proof. A missing proof is fail-closed and must be
   reported as a tooling gap, not treated as success.
5. Planner decides the correction scope. The Agent changes only the resulting
   server-owned Task/Attempt worktree, runs the required focused gates, and
   leaves final checkpoint/finalize/review to the canonical lifecycle.

For a rejected review, retain the original Attempt and review as immutable
history. A correction must identify its rejected source and prove the new
source/checkpoint relationship before it can be dispatched.
