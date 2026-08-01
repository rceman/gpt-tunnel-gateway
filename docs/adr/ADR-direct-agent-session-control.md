# ADR: Direct project-agent session control

Status: accepted for v0.6.0

## Decision

Direct session communication is a separate control surface from the durable
task/run workflow. The project-level operations are:

- `agent_send(project_id, message)`;
- `agent_tail(project_id, lines=4, skip=0)`;
- `agent_status(project_id)`.

CLI equivalents are `gpt-tunnel agent send`, `agent tail`, and `agent status`.
The project ID resolves only through configured project metadata; callers may
not supply an arbitrary Airelay session key. Generic shell execution is not a
capability of the gateway.

Direct operations create no task, run, plan, result, report, branch, commit,
or other Git mutation. Sends are serialized per configured session, message
and output sizes are bounded, and there is no implicit retry or automatic
continue. The exact Airelay exit code and bounded output are returned. Status
preserves waiting/running/idle/error and capacity warnings.

## Rationale

GPT sometimes needs a short follow-up to an already registered project agent.
Routing that message through task creation would create false workflow records
and obscure ownership. A narrow typed session API keeps direct control
observable and separate while retaining the durable workflow as the only path
for authorized coding work.
