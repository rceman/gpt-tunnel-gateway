# Run review snapshot review-r2 evidence

This proof-closure change is test-only. It adds dedicated native coverage for
the review snapshot structural branches, Git ref classification, MCP tools/call
contract, CLI JSON/error rendering, deterministic ordering, output bounds,
canonical report hub history, and path redaction. No production files were
changed.

Base: `9a40d1cc14f4847dd3e43d1c487e1c039bc1bd8f`
Implementation: `c000bbbc610044df5479965154a58aaa48120a48`
Evidence commit: recorded separately after this file is staged.

Test-to-branch mapping:

- `TestSnapshotMergedRunWithDeletedTaskBranch` → merged publication proof.
- `TestSnapshotMissingReport`, `TestSnapshotMissingEvidence` → terminal artifact failures.
- `TestSnapshotTaskRunHashBaseBranchIdentityMismatch`, `TestSnapshotTaskStateMismatch` → task/run/hash/base/branch/task-state identity failures.
- `TestSnapshotReportRunAndStatusIdentityMismatch`, `TestSnapshotEvidenceIdentityMismatch` → report/evidence identity failures.
- `TestSnapshotMirrorRefreshFailure`, `TestSnapshotDefaultRefResolutionFailure`, `TestSnapshotTaskRefResolutionFailureDistinctFromMissing` → Git component and ref-error branches.
- `TestSnapshotUnreachableEvidenceHead`, `TestSnapshotNonAncestorBase`, `TestSnapshotUnpublishedUnmergedBranch` → reachability, ancestry, and publication failures.
- `TestSnapshotDirtyWorktree`, `TestSnapshotMismatchedWorktreeBranchOrHead`, `TestSnapshotMergedWorktreeHeadNotContainingEvidence` → worktree consistency branches.
- `TestSnapshotChangedFileCalculationFailure`, `TestSnapshotChangedFileMismatch`, `TestSnapshotDiffStatFailure` → changed-file and diff-stat branches.
- `TestSnapshotMissingRequiredGate`, `TestSnapshotFailingRequiredGate` → required-gate failures.
- `TestSnapshotCanonicalReportHubCommit`, `TestSnapshotChecksAreDeterministicallyOrdered`, `TestSnapshotRedactsInternalPathsAndGitCommands` → hub proof, ordering, and redaction.
- `TestRunReviewSnapshotRejectsOversizedAggregate` → aggregate output bound.
- `TestRunReviewSnapshotToolCallUsesOnlyRunIDAndReturnsToolErrorForUnknownRun` plus existing MCP schema/annotation tests → tools/call/input/output contract.
- `TestReviewSnapshotCLISuccessRenderingPath`, `TestReviewSnapshotCLIErrorRenderingPathIsBounded` → CLI success/error rendering.
- Existing `TestRunReviewSnapshotActiveIsBounded` and `TestTaskPlanDispatchReadFinalize` retain active and terminal-reviewable integration coverage.

All required repository gates passed. No merge, installation, deployment,
runtime restart, tunnel mutation, release/tag, main update, or connector
refresh occurred.
