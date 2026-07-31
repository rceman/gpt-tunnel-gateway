# Active-run Airelay tail evidence

Resumed the existing run `125c94f1-68a4-491d-887d-8c399f8e1397` on its
existing branch and preserved the supplied worktree. The in-scope compile
failure was fixed by adding the missing hub test import. Two generated
`hub-repository.lock` files were classified as PID/timestamp artifacts from
tests using an empty `StateDir`; only those artifacts were removed.

Implementation: `8f0ecf517a6c9ab8928b791148ffb3abba1438b4`
Evidence commit: recorded separately after this file is staged.

The feature adds the bounded active-run Airelay tail service, MCP, and CLI
operation with exact argv, ownership/active checks, line bounds, plain-text
MCP transport, control normalization, fake-Airelay coverage, and synchronized
0.4.0 versions.

Test mapping:

- `TestTailUsesExactArgumentsAndNormalizesFixture`, `TestTailExplicitLinesAndFailuresAreBounded`, `TestTailTimeoutDoesNotExposeSession`, and `TestTailRejectsEmptyAndOversizedOutput` cover Airelay argv, fixture preservation, errors, timeout, redaction, and bounded capture.
- `TestRunAgentTailUsesStoredSessionAndDefaultAndExplicitLines` covers service ownership of the stored session and default/explicit lines.
- `TestRunAgentTailRejectsBoundsTerminalAndForeignBeforeAirelay` covers bounds, terminal rejection, and foreign-gateway rejection before invocation.
- `TestRunAgentTailSuccessIsPlainTextWithoutStructuredContent`, `TestRunReviewSnapshotToolCallUsesOnlyRunIDAndReturnsToolErrorForUnknownRun`, and the existing tools/list/schema/annotation tests cover MCP transport and contract behavior.
- Existing CLI tests plus the `agent-tail` route cover normalized success rendering and bounded errors.

Required formatting, vet, race, static, release checksum, 0.4.0 binary, and
diff gates passed. No merge, install, deployment, release/tag, runtime,
tunnel, connector, or ntfy action occurred.
