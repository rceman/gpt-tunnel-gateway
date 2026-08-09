package mcp

import (
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (f *canonicalOutputFixture) populateCanonicalReview() {
	now := f.now
	task := f.task.(model.Task)
	worktree := f.worktree.(gitx.WorktreeStatus)
	ref := gitx.Ref{Name: "refs/heads/main", ObjectType: "commit", ObjectName: worktree.Head}
	commit := gitx.Commit{SHA: worktree.Head, Parents: []string{}, AuthorName: "GPT", AuthorEmail: "gpt@example.invalid", AuthorDate: now.Format(time.RFC3339), Subject: "subject"}
	compare := gitx.Compare{MergeBase: worktree.Head, LeftOnly: 0, RightOnly: 0}
	clean := true
	reviewState := model.ReviewRepositoryState{Branch: "feature/x", BaseRevision: task.BaseRevision, ReviewedHead: worktree.Head, WorktreeClean: true, BaseAncestor: true}
	reviewReport := model.RunReviewReport{SchemaVersion: model.RunReviewReportSchemaVersion, ID: "run-REPORT", TaskID: "task", RunID: "run", ProjectID: "project", TaskSHA256: task.SHA256, Branch: "feature/x", BaseRevision: task.BaseRevision, ReviewedHead: worktree.Head, Outcome: model.ReviewOutcomeAccepted, RepositoryState: reviewState, Gates: []model.CompletionGateResult{}, Findings: []model.ReviewFinding{}, ScopeCoverage: []model.ReviewScopeCoverage{}, ChangedFiles: []string{}, UnexpectedSurfaces: []string{}, HistoricalCompatibility: []string{}, ProhibitedActions: []string{}, NextAction: "reviewed_merge_ready", FinishedAt: now}
	reviewDraft := model.RunReviewReportDraft{SchemaVersion: model.RunReviewReportSchemaVersion, ID: "run-REPORT", TaskID: "task", RunID: "run", ProjectID: "project", TaskSHA256: task.SHA256, Branch: "feature/x", BaseRevision: task.BaseRevision, ReviewedHead: worktree.Head, RepositoryState: reviewState, Gates: []model.CompletionGateResult{}, Findings: []model.ReviewFinding{}, ScopeCoverage: []model.ReviewScopeCoverage{}, ChangedFiles: []string{}, UnexpectedSurfaces: []string{}, HistoricalCompatibility: []string{}, ProhibitedActions: []string{}, CompletedSections: model.RunReviewReportSections, DraftRevision: 1, UpdatedAt: now}
	reviewValidation := model.RunReviewValidation{Valid: true, Errors: []string{}, Draft: reviewDraft}
	snapshot := model.ReviewSnapshot{SchemaVersion: 1, Run: model.ReviewSnapshotRun{ID: "run", TaskID: "task", ProjectID: "project", Status: "succeeded", CreatedAt: now}, Task: model.ReviewSnapshotTask{ID: "task", SHA256: task.SHA256, Title: "title", Objective: "objective", AcceptanceCriteria: []string{}, Constraints: []string{}, RequiredGates: []string{}, CreatedBy: "gpt", CreatedAt: now, TaskStateStatus: "completed"}, Report: model.ReviewSnapshotReport{Available: true, Status: "succeeded", Summary: "done", Commits: []string{}, ChangedFiles: []string{}, GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{}, FinishedAt: &now}, Evidence: model.ReviewSnapshotEvidence{Available: true, Head: worktree.Head, Branch: "feature/x", WorktreeClean: &clean, Notes: []string{}, RecordedAt: &now}, Repository: model.ReviewSnapshotRepo{RefreshAttempted: true, RefreshSucceeded: true, DefaultBranch: "main", TaskBranch: "feature/x", TaskBranchPublished: true, TaskBranchHead: worktree.Head, Worktree: model.ReviewSnapshotWorktree{Branch: "feature/x", Head: worktree.Head, Clean: true}, EvidenceHeadReachable: true, BaseToEvidence: model.ReviewSnapshotCompare{MergeBase: task.BaseRevision}, DefaultToEvidence: model.ReviewSnapshotCompare{}, ChangedFiles: []string{}}, Checks: []model.ReviewSnapshotCheck{}, ReviewState: "reviewable", NextAction: "perform_static_review"}
	f.ref = ref
	f.commit = commit
	f.compare = compare
	f.clean = clean
	f.reviewState = reviewState
	f.reviewReport = reviewReport
	f.reviewDraft = reviewDraft
	f.reviewValidation = reviewValidation
	f.snapshot = snapshot
}
