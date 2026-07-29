# Behavior contract

The required order is task create, plan update, task dispatch. Dispatch publishes
and verifies state before directly calling `airelay prompt <session-key> <message>`;
it never starts/resumes sessions. Delivery is non-terminal. Finalization is
local-agent-only, validates repository state and bounded evidence, and writes a
deterministic report. Failure, cancellation, timeout, and dispatch failure still
produce a terminal result.
