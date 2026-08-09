package service

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type snapshotMatrixFixture struct {
	run      model.Run
	task     model.Task
	state    model.TaskState
	report   model.ReviewSnapshotReport
	evidence model.ReviewSnapshotEvidence
	repo     model.ReviewSnapshotRepo
}

func newSnapshotMatrixFixture() snapshotMatrixFixture {
	now := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	task := model.Task{ID: "task", SHA256: strings.Repeat("c", 64), ProjectID: "example", Branch: "feature/review", BaseRevision: base, RequiredGates: []string{"go test ./..."}, CreatedAt: now}
	run := model.Run{ID: "run", TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, Branch: task.Branch, BaseRevision: task.BaseRevision, Status: "succeeded", CreatedAt: now}
	clean := true
	return snapshotMatrixFixture{
		run:      run,
		task:     task,
		state:    model.TaskState{TaskID: task.ID, TaskSHA256: task.SHA256, Status: "completed", UpdatedAt: now},
		report:   model.ReviewSnapshotReport{Available: true, Status: "succeeded", ChangedFiles: []string{"file.go"}, GateResults: []model.CompletionGateResult{{ID: "G1", ExitCode: 0}}, HubCommit: strings.Repeat("d", 40)},
		evidence: model.ReviewSnapshotEvidence{Available: true, Head: head, Branch: task.Branch, WorktreeClean: &clean},
		repo:     model.ReviewSnapshotRepo{RefreshSucceeded: true, DefaultBranch: "main", DefaultHead: head, TaskBranch: task.Branch, TaskBranchPublished: true, TaskBranchHead: head, Worktree: model.ReviewSnapshotWorktree{Branch: task.Branch, Head: head, Clean: true}, EvidenceHeadReachable: true, BaseToEvidence: model.ReviewSnapshotCompare{MergeBase: base}, DefaultToEvidence: model.ReviewSnapshotCompare{MergeBase: head}, ChangedFiles: []string{"file.go"}},
	}
}

func matrixChecks(f snapshotMatrixFixture, terminal bool, errs ...error) []model.ReviewSnapshotCheck {
	var taskErr, stateErr, reportErr, evidenceErr error
	if len(errs) > 0 {
		taskErr = errs[0]
	}
	if len(errs) > 1 {
		stateErr = errs[1]
	}
	if len(errs) > 2 {
		reportErr = errs[2]
	}
	if len(errs) > 3 {
		evidenceErr = errs[3]
	}
	return snapshotChecks(f.run, f.task, f.state, taskErr, stateErr, reportErr, evidenceErr, f.report, f.evidence, f.repo, terminal)
}

func matrixCheck(t *testing.T, checks []model.ReviewSnapshotCheck, id string) model.ReviewSnapshotCheck {
	t.Helper()
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("missing check %q: %#v", id, checks)
	return model.ReviewSnapshotCheck{}
}

func TestSnapshotMergedRunWithDeletedTaskBranch(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.TaskBranchPublished = false
	f.repo.TaskBranchHead = ""
	f.repo.TaskBranchError = "task branch ref is missing"
	f.repo.Worktree = model.ReviewSnapshotWorktree{Branch: "main", Head: f.repo.DefaultHead, Clean: true}
	if got := matrixCheck(t, matrixChecks(f, true), "branch_publication"); got.Status != "pass" {
		t.Fatalf("%#v", got)
	}
}

func TestSnapshotMissingReport(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.report = model.ReviewSnapshotReport{Available: false}
	if got := matrixCheck(t, matrixChecks(f, true, nil, nil, errMatrix("missing report")), "report_identity"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}

func TestSnapshotMissingEvidence(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.evidence = model.ReviewSnapshotEvidence{Available: false}
	if got := matrixCheck(t, matrixChecks(f, true, nil, nil, nil, errMatrix("missing evidence")), "evidence_identity"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}

func TestSnapshotTaskRunHashBaseBranchIdentityMismatch(t *testing.T) {
	fields := []struct {
		name   string
		mutate func(*snapshotMatrixFixture)
	}{
		{"task", func(f *snapshotMatrixFixture) { f.task.ID = "other" }},
		{"run", func(f *snapshotMatrixFixture) { f.run.TaskID = "other" }},
		{"hash", func(f *snapshotMatrixFixture) { f.run.TaskSHA256 = strings.Repeat("e", 64) }},
		{"base", func(f *snapshotMatrixFixture) { f.run.BaseRevision = strings.Repeat("f", 40) }},
		{"branch", func(f *snapshotMatrixFixture) { f.run.Branch = "feature/other" }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			f := newSnapshotMatrixFixture()
			field.mutate(&f)
			if got := matrixCheck(t, matrixChecks(f, true, errMatrix("identity mismatch")), "identity_consistency"); got.Status != "fail" {
				t.Fatalf("%#v", got)
			}
		})
	}
}

func TestSnapshotTaskStateMismatch(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.state.TaskSHA256 = strings.Repeat("e", 64)
	if got := matrixCheck(t, matrixChecks(f, true, nil, errMatrix("task state mismatch")), "identity_consistency"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}

func TestSnapshotReportRunAndStatusIdentityMismatch(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.report = model.ReviewSnapshotReport{Available: false}
	if got := matrixCheck(t, matrixChecks(f, true, nil, nil, errMatrix("report identity")), "report_identity"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}

func TestSnapshotEvidenceIdentityMismatch(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.evidence = model.ReviewSnapshotEvidence{Available: false}
	if got := matrixCheck(t, matrixChecks(f, true, nil, nil, nil, errMatrix("evidence identity")), "evidence_identity"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}

func TestSnapshotMirrorRefreshFailure(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.RefreshSucceeded = false
	f.repo.RefreshError = "refresh failed"
	if got := matrixCheck(t, matrixChecks(f, true), "mirror_refresh"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotDefaultRefResolutionFailure(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.DefaultHead = ""
	f.repo.DefaultHeadError = "default ref failed"
	if got := matrixCheck(t, matrixChecks(f, true), "default_head_component"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotTaskRefResolutionFailureDistinctFromMissing(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.TaskBranchPublished = false
	f.repo.TaskBranchError = "task ref failed"
	if got := matrixCheck(t, matrixChecks(f, true), "task_branch_component"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotUnreachableEvidenceHead(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.EvidenceHeadReachable = false
	f.repo.EvidenceHeadError = "evidence head missing"
	if got := matrixCheck(t, matrixChecks(f, true), "evidence_head_reachable"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotNonAncestorBase(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.BaseToEvidence = model.ReviewSnapshotCompare{Error: "not ancestor"}
	if got := matrixCheck(t, matrixChecks(f, true), "base_ancestor"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotUnpublishedUnmergedBranch(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.TaskBranchPublished = false
	f.repo.TaskBranchError = "task branch ref is missing"
	f.repo.DefaultToEvidence = model.ReviewSnapshotCompare{MergeBase: strings.Repeat("a", 40), RightOnly: 1}
	if got := matrixCheck(t, matrixChecks(f, true), "branch_publication"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotDirtyWorktree(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.Worktree.Clean = false
	if got := matrixCheck(t, matrixChecks(f, true), "worktree_consistency"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotMismatchedWorktreeBranchOrHead(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.Worktree.Branch = "main"
	if got := matrixCheck(t, matrixChecks(f, true), "worktree_consistency"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotMergedWorktreeHeadNotContainingEvidence(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.TaskBranchPublished = false
	f.repo.TaskBranchError = "task branch ref is missing"
	f.repo.Worktree = model.ReviewSnapshotWorktree{Branch: "main", Head: strings.Repeat("e", 40), Clean: true}
	if got := matrixCheck(t, matrixChecks(f, true), "worktree_consistency"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotChangedFileCalculationFailure(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.ChangedFilesError = "changed files failed"
	if got := matrixCheck(t, matrixChecks(f, true), "changed_file_equality"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotChangedFileMismatch(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.ChangedFiles = []string{"other.go"}
	if got := matrixCheck(t, matrixChecks(f, true), "changed_file_equality"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotDiffStatFailure(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.repo.DiffStatError = "diff stat failed"
	if got := matrixCheck(t, matrixChecks(f, true), "diff_stat_component"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotMissingRequiredGate(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.report.GateResults = nil
	if got := matrixCheck(t, matrixChecks(f, true), "required_gates"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotFailingRequiredGate(t *testing.T) {
	f := newSnapshotMatrixFixture()
	f.report.GateResults[0].ExitCode = 1
	if got := matrixCheck(t, matrixChecks(f, true), "required_gates"); got.Status != "fail" {
		t.Fatalf("%#v", got)
	}
}
func TestSnapshotCanonicalReportHubCommit(t *testing.T) {
	f := newSnapshotMatrixFixture()
	if f.report.HubCommit == "" {
		t.Fatal("hub commit proof missing")
	}
}
func TestSnapshotChecksAreDeterministicallyOrdered(t *testing.T) {
	f := newSnapshotMatrixFixture()
	checks := matrixChecks(f, true)
	for i := 1; i < len(checks); i++ {
		if checks[i-1].ID > checks[i].ID {
			t.Fatalf("checks not sorted: %#v", checks)
		}
	}
}
func TestSnapshotRedactsInternalPathsAndGitCommands(t *testing.T) {
	detail := snapshotDetail(errMatrix("git show /home/user/private/result.json failed"))
	if strings.Contains(detail, "/home/") || strings.Contains(detail, "git show") {
		t.Fatalf("unsafe detail: %q", detail)
	}
}

type matrixError string

func (e matrixError) Error() string { return string(e) }
func errMatrix(s string) error      { return matrixError(s) }
