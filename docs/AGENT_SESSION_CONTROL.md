# Direct agent session control

The v0.6.0 direct control surface addresses only a registered configured
project. The gateway derives its canonical Airelay session key from project
metadata and never accepts a caller-supplied key.

```text
gpt-tunnel agent send <project-id> --text '<short message>'
gpt-tunnel agent tail <project-id> --lines 4 --skip 0
gpt-tunnel agent status <project-id>
```

The equivalent MCP tools are `agent_send`, `agent_tail`, and `agent_status`.
Messages, output, lines, and skip are bounded. `agent_tail` defaults to four
lines; `skip` omits the newest lines from the one requested bounded window.
Sends are serialized per session. Calls return exact delivery/exit information
and do not retry. Status normalizes Airelay's busy/working state to `running`,
and preserves bounded capacity warnings.

These operations do not create tasks or runs and do not mutate plans, Git, or
the hub. Use the durable task workflow when implementation authorization and
completion evidence are required.
