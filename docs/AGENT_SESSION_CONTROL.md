# Direct agent session control

The v0.6.0 direct control surface addresses only a registered configured
project. The gateway derives its canonical Airelay session key from project
metadata and never accepts a caller-supplied key.

```text
gpt-tunnel agent send <project-id> --text '<short message>'
gpt-tunnel agent tail <project-id> --lines 10
gpt-tunnel agent transcript <project-id> --lines 50 --skip 0
gpt-tunnel agent status <project-id>
```

The equivalent MCP tools are `agent_send`, `agent_tail`, `agent_transcript`, and
`agent_status`. Messages and output are bounded. `agent_tail` reads the current
Airelay viewport, defaults to 10 lines, accepts 1..30 or `-1` for the full
30-row viewport, and has no skip/history behavior. `agent_transcript` reads
bounded retained history, defaults to 50 lines, supports bounded `skip`, and
never supplies liveness authority.
Sends are serialized per session. Calls return exact delivery/exit information
and do not retry. Status normalizes Airelay's busy/working state to `running`,
and preserves bounded capacity warnings.

These operations do not create tasks or runs and do not mutate plans, Git, or
the hub. Use the durable task workflow when implementation authorization and
completion evidence are required.

`agent_send` is bounded emergency/control-plane communication only. It may
request a short diagnostic or acknowledge an operator-directed control step;
it never authorizes new work, changes task scope, approves a merge or release,
or substitutes for durable task creation and finalization. Misuse examples that
must remain in the durable task workflow include:

```text
agent_send(project, "Implement the next feature")       # new scope
agent_send(project, "Merge and release this branch")     # merge/release authority
agent_send(project, "Deploy this and change production")  # deployment authority
agent_send(project, "Continue all remaining roadmap work") # implicit authorization
```

## Liveness and context recovery

`gpt-tunnel project status <project-id>` is the normal bounded progress check.
It includes the compact plan and worktree facts, latest task/run, normalized
agent state, controller reachability, capacity/rate-limit warnings, a four-line
tail, last meaningful activity, blocker classification, and one recommended
next action. Healthy bounded status, tail, repository and hub components are
collected concurrently; partial failures return safe component error codes. It
never exposes the configured session key and never sends a prompt.

The liveness vocabulary is: `idle`, `running`, `waiting_for_input`,
`compacting`, `compacted_resuming`, `compacted_idle`, `capacity_blocked`,
`rate_limited`, `completion_pending`, `finalization_pending`, `stalled`,
`error`, and `unknown`. A compaction stall requires an active operational run,
a reachable session, a completed compaction marker, no meaningful output after
it, no unanswered question, and a nonterminal run. A low-context warning alone
is never sufficient.

`gpt-tunnel run resume <run-id>` generates a bounded recovery instruction that
re-reads the immutable task packet, inspects branch/base/HEAD/worktree/commits
and durable run state, preserves scope, skips completed phases, and proceeds to
verification/finalization when complete. `run_sweep` may invoke this operation
once after confirmed compaction. Repeated automatic resumes are forbidden.
Operational compaction/resume events are bounded local evidence only; they are
not a second completion, report, or task authority.
