# Gateway procedure index

| Procedure | Command | Mutation |
| --- | --- | --- |
| Inspect upgrade | `gpt-tunnelctl upgrade inspect` | none |
| Diagnose startup | `gpt-tunnelctl diagnose-startup` | none |
| Check state | `gpt-tunnelctl state check` | none |
| Preview repair | `gpt-tunnelctl state repair --dry-run` | none |
| Apply repair | `gpt-tunnelctl state repair --apply` | one backed-up hub transaction |
| Upgrade | `gpt-tunnelctl upgrade` | durable transactional, gateway-only |
| Direct agent send | `gpt-tunnel agent send` | Airelay session only |
| Direct agent tail | `gpt-tunnel agent tail` | read-only |
| Direct agent status | `gpt-tunnel agent status` | read-only |

Read the relevant runbook before any mutating procedure. A gateway upgrade
never restarts tunnel-client. After two activation failures, use diagnosis-only
mode until an exact root cause is established.
