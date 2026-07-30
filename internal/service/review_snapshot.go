package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

var snapshotPathRE = regexp.MustCompile(`/[^ ]+`)

func snapshotDetail(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	s = snapshotPathRE.ReplaceAllString(s, "<path>")
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

func (s *Service) RunReviewSnapshot(ctx context.Context, id string) (model.ReviewSnapshot, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return model.ReviewSnapshot{}, err
	}
	task, err := s.readTaskForRun(ctx, run)
	if err != nil {
		return model.ReviewSnapshot{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.ReviewSnapshot{}, err
	}
	snapshot := model.ReviewSnapshot{SchemaVersion: 1,
		Run:    model.ReviewSnapshotRun{ID: run.ID, TaskID: run.TaskID, ProjectID: run.ProjectID, Status: run.Status, Branch: run.Branch, BaseRevision: run.BaseRevision, CreatedAt: run.CreatedAt, DispatchedAt: run.DispatchedAt, FinishedAt: run.FinishedAt},
		Task:   model.ReviewSnapshotTask{ID: task.ID, SHA256: task.SHA256, Title: task.Title, Objective: task.Objective, Branch: task.Branch, BaseRevision: task.BaseRevision, AcceptanceCriteria: append([]string{}, task.AcceptanceCriteria...), Constraints: append([]string{}, task.Constraints...), RequiredGates: append([]string{}, task.RequiredGates...), CreatedBy: task.CreatedBy, CreatedAt: task.CreatedAt, TaskStateStatus: state.Status},
		Checks: []model.ReviewSnapshotCheck{},
	}
	terminal := !activeStatus(run.Status)
	report, reportErr := s.readSnapshotReport(ctx, run, task)
	evidence, evidenceErr := s.readSnapshotEvidence(ctx, run, task)
	if !terminal {
		report = model.ReviewSnapshotArtifact{Available: false}
		evidence = model.ReviewSnapshotArtifact{Available: false}
	}
	if reportErr != nil && terminal {
		report.Error = snapshotDetail(reportErr)
	}
	if evidenceErr != nil && terminal {
		evidence.Error = snapshotDetail(evidenceErr)
	}
	snapshot.Report, snapshot.Evidence = report, evidence

	project, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.ReviewSnapshot{}, err
	}
	repo := model.ReviewSnapshotRepo{RefreshAttempted: true, DefaultBranch: project.DefaultBranch, TaskBranch: run.Branch, ChangedFiles: []string{}}
	if err := s.Git.Refresh(ctx, project); err != nil {
		repo.RefreshError = snapshotDetail(err)
	} else {
		repo.RefreshSucceeded = true
		s.fillSnapshotRepository(ctx, project, run, snapshot.Evidence, snapshot.Report, &repo)
	}
	snapshot.Repository = repo
	snapshot.Checks = snapshotChecks(run, task, state, snapshot.Report, snapshot.Evidence, repo, terminal)
	snapshot.ReviewState = "active"
	snapshot.NextAction = "wait_for_terminal"
	if terminal {
		snapshot.ReviewState = "reviewable"
		snapshot.NextAction = "perform_static_review"
		for _, check := range snapshot.Checks {
			if check.Severity == "critical" && check.Status == "fail" {
				snapshot.ReviewState = "blocked"
				snapshot.NextAction = "resolve_structural_failure"
				break
			}
		}
	}
	return snapshot, nil
}

func (s *Service) readTaskForRun(ctx context.Context, run model.Run) (model.Task, error) {
	var task model.Task
	if err := s.Hub.ReadJSON(ctx, s.taskPath(run.ProjectID, run.TaskID), &task); err != nil {
		return task, err
	}
	if task.ID != run.TaskID || task.ProjectID != run.ProjectID || task.SHA256 != run.TaskSHA256 {
		return task, fmt.Errorf("task identity mismatch")
	}
	return task, nil
}

func (s *Service) readSnapshotReport(ctx context.Context, run model.Run, task model.Task) (model.ReviewSnapshotArtifact, error) {
	var report model.Report
	if err := s.Hub.ReadJSON(ctx, s.reportPath(run.ProjectID, run.ID), &report); err != nil {
		return model.ReviewSnapshotArtifact{}, err
	}
	if report.SchemaVersion != model.SchemaVersion || report.TaskID != task.ID || report.RunID != run.ID || report.ProjectID != run.ProjectID || report.Status == "" || report.FinishedAt.IsZero() {
		return model.ReviewSnapshotArtifact{}, fmt.Errorf("report identity or completeness mismatch")
	}
	return model.ReviewSnapshotArtifact{Available: true, Status: report.Status, Summary: report.Summary, Commits: model.CanonicalStrings(report.Commits), ChangedFiles: model.CanonicalStrings(report.ChangedFiles), Commands: report.Commands, Deviations: report.Deviations, RemainingRisks: report.RemainingRisks, FinishedAt: &report.FinishedAt, HubCommit: report.HubCommit}, nil
}

func (s *Service) readSnapshotEvidence(ctx context.Context, run model.Run, task model.Task) (model.ReviewSnapshotArtifact, error) {
	var evidence model.Evidence
	if err := s.Hub.ReadJSON(ctx, s.evidencePath(run.ProjectID, run.ID), &evidence); err != nil {
		return model.ReviewSnapshotArtifact{}, err
	}
	if err := model.ValidateEvidence(evidence, task, run); err != nil {
		return model.ReviewSnapshotArtifact{}, err
	}
	return model.ReviewSnapshotArtifact{Available: true, Head: evidence.ProjectHead, Branch: evidence.Branch, WorktreeClean: &evidence.WorktreeClean, Notes: evidence.Notes, RecordedAt: &evidence.RecordedAt}, nil
}

func (s *Service) fillSnapshotRepository(ctx context.Context, p config.ProjectConfig, run model.Run, evidence, report model.ReviewSnapshotArtifact, repo *model.ReviewSnapshotRepo) {
	wt, err := s.Git.WorktreeStatus(ctx, p)
	if err == nil {
		repo.Worktree = model.ReviewSnapshotWorktree{Branch: wt.Branch, Head: wt.Head, Upstream: wt.Upstream, Ahead: wt.Ahead, Behind: wt.Behind, Clean: wt.Clean}
	}
	defaultHead, err := s.Git.ResolveMirrorRef(ctx, p, "refs/remotes/origin/"+p.DefaultBranch)
	if err != nil {
		defaultHead, _ = s.Git.ResolveMirrorRef(ctx, p, "refs/heads/"+p.DefaultBranch)
	}
	repo.DefaultHead = defaultHead
	taskHead, err := s.Git.ResolveMirrorRef(ctx, p, "refs/heads/"+run.Branch)
	if err != nil {
		taskHead, err = s.Git.ResolveMirrorRef(ctx, p, "refs/remotes/origin/"+run.Branch)
	}
	if err == nil {
		repo.TaskBranchPublished, repo.TaskBranchHead = true, taskHead
	}
	if evidence.Head != "" {
		_, err = s.Git.ResolveMirrorRef(ctx, p, evidence.Head)
		repo.EvidenceHeadReachable = err == nil
		if repo.EvidenceHeadReachable {
			repo.ChangedFiles, _ = s.Git.MirrorChangedFiles(ctx, p, run.BaseRevision, evidence.Head)
			repo.DiffStat, _ = s.Git.MirrorDiffStat(ctx, p, run.BaseRevision, evidence.Head)
		}
		if run.BaseRevision != "" {
			if c, e := s.Git.MirrorCompare(ctx, p, run.BaseRevision, evidence.Head); e == nil {
				repo.BaseToEvidence = model.ReviewSnapshotCompare{MergeBase: c.MergeBase, LeftOnly: c.LeftOnly, RightOnly: c.RightOnly}
			} else {
				repo.BaseToEvidence.Error = snapshotDetail(e)
			}
		}
		if defaultHead != "" {
			if c, e := s.Git.MirrorCompare(ctx, p, defaultHead, evidence.Head); e == nil {
				repo.DefaultToEvidence = model.ReviewSnapshotCompare{MergeBase: c.MergeBase, LeftOnly: c.LeftOnly, RightOnly: c.RightOnly}
			} else {
				repo.DefaultToEvidence.Error = snapshotDetail(e)
			}
		}
	}
}

func snapshotChecks(run model.Run, task model.Task, state model.TaskState, report, evidence model.ReviewSnapshotArtifact, repo model.ReviewSnapshotRepo, terminal bool) []model.ReviewSnapshotCheck {
	checks := []model.ReviewSnapshotCheck{}
	add := func(id, severity, status, detail string) {
		checks = append(checks, model.ReviewSnapshotCheck{ID: id, Severity: severity, Status: status, Detail: detail})
	}
	if !terminal {
		add("terminal_artifacts", "critical", "not_applicable", "run is active")
	} else if report.Available && evidence.Available {
		add("terminal_artifacts", "critical", "pass", "report and evidence are available")
	} else {
		add("terminal_artifacts", "critical", "fail", "terminal report or evidence is unavailable")
	}
	if task.ID == run.TaskID && task.ProjectID == run.ProjectID && task.SHA256 == run.TaskSHA256 && task.Branch == run.Branch {
		add("identity_consistency", "critical", "pass", "run, task, and task-state identities agree")
	} else {
		add("identity_consistency", "critical", "fail", "run and task identities disagree")
	}
	if repo.RefreshSucceeded {
		add("mirror_refresh", "critical", "pass", "managed mirror refreshed once")
	} else {
		add("mirror_refresh", "critical", "fail", repo.RefreshError)
	}
	if repo.EvidenceHeadReachable {
		add("evidence_head_reachable", "critical", "pass", "evidence head is reachable in mirror")
	} else if terminal {
		add("evidence_head_reachable", "critical", "fail", "evidence head is not reachable")
	} else {
		add("evidence_head_reachable", "critical", "not_applicable", "run is active")
	}
	if repo.BaseToEvidence.Error == "" && repo.BaseToEvidence.MergeBase != "" && repo.BaseToEvidence.LeftOnly == 0 {
		add("base_ancestor", "critical", "pass", "base is an ancestor of evidence head")
	} else if terminal {
		add("base_ancestor", "critical", "fail", "base is not an ancestor of evidence head")
	} else {
		add("base_ancestor", "critical", "not_applicable", "run is active")
	}
	publication := repo.TaskBranchPublished && repo.TaskBranchHead == evidence.Head
	merged := !repo.TaskBranchPublished && repo.DefaultHead != "" && repo.DefaultToEvidence.Error == "" && repo.DefaultToEvidence.RightOnly == 0
	if publication || merged {
		add("branch_publication", "critical", "pass", "task branch is published or evidence is merged")
	} else if terminal {
		add("branch_publication", "critical", "fail", "task branch is unpublished and evidence is not merged")
	} else {
		add("branch_publication", "critical", "not_applicable", "run is active")
	}
	worktreeOK := repo.Worktree.Clean && ((repo.TaskBranchPublished && repo.Worktree.Branch == run.Branch && repo.Worktree.Head == evidence.Head) || (merged && repo.Worktree.Branch == repo.DefaultBranch && repo.Worktree.Head != ""))
	if worktreeOK {
		add("worktree_consistency", "critical", "pass", "worktree is clean and consistent")
	} else if terminal {
		add("worktree_consistency", "critical", "fail", "worktree does not match review state")
	} else {
		add("worktree_consistency", "critical", "not_applicable", "run is active")
	}
	if !terminal {
		add("changed_file_equality", "critical", "not_applicable", "run is active")
	} else if report.Available && strings.Join(repo.ChangedFiles, "\x00") == strings.Join(report.ChangedFiles, "\x00") {
		add("changed_file_equality", "critical", "pass", "actual and reported changed files agree")
	} else {
		add("changed_file_equality", "critical", "fail", "actual and reported changed files differ")
	}
	if !terminal {
		add("required_gates", "critical", "not_applicable", "run is active")
	} else if report.Available {
		missing := []string{}
		for _, gate := range task.RequiredGates {
			found := false
			for _, command := range report.Commands {
				if command.Command == gate && command.ExitCode == 0 {
					found = true
				}
			}
			if !found {
				missing = append(missing, gate)
			}
		}
		if len(missing) == 0 {
			add("required_gates", "critical", "pass", "all required gates are present and passing")
		} else {
			add("required_gates", "critical", "fail", "required gates missing or failing")
		}
	} else {
		add("required_gates", "critical", "fail", "report unavailable")
	}
	if !terminal {
		add("clean_evidence", "critical", "not_applicable", "run is active")
	} else if evidence.Available && evidence.WorktreeClean != nil && *evidence.WorktreeClean {
		add("clean_evidence", "critical", "pass", "evidence reports a clean worktree")
	} else {
		add("clean_evidence", "critical", "fail", "evidence is not clean")
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
	return checks
}
