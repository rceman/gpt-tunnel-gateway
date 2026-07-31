package service

import (
	"context"
	"encoding/json"
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
	if strings.Contains(s, "git ") {
		s = "Git component operation failed"
	}
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
	if run.Historical {
		return model.ReviewSnapshot{}, fmt.Errorf("workflow-v1 run review snapshot is history-only")
	}
	task, taskErr := s.readTaskForRun(ctx, run)
	state, stateErr := s.taskState(ctx, task)
	if stateErr != nil {
		state = model.TaskState{TaskID: task.ID, TaskSHA256: task.SHA256, Status: "unknown"}
	}
	snapshot := model.ReviewSnapshot{SchemaVersion: 1,
		Run:    model.ReviewSnapshotRun{ID: run.ID, TaskID: run.TaskID, ProjectID: run.ProjectID, Status: run.Status, Branch: run.Branch, BaseRevision: run.BaseRevision, CreatedAt: run.CreatedAt, DispatchedAt: run.DispatchedAt, FinishedAt: run.FinishedAt},
		Task:   model.ReviewSnapshotTask{ID: task.ID, SHA256: task.SHA256, Title: task.Title, Objective: task.Objective, Branch: task.Branch, BaseRevision: task.BaseRevision, AcceptanceCriteria: snapshotStrings(task.AcceptanceCriteria), Constraints: snapshotStrings(task.Constraints), RequiredGates: snapshotStrings(task.RequiredGates), CreatedBy: task.CreatedBy, CreatedAt: task.CreatedAt, TaskStateStatus: state.Status},
		Checks: []model.ReviewSnapshotCheck{},
	}
	terminal := !activeStatus(run.Status)
	report, reportErr := s.readSnapshotReport(ctx, run, task)
	evidence, evidenceErr := snapshotEvidenceFromReport(report, reportErr)
	if terminal && reportErr == nil {
		wantState := "ready"
		if report.Status == "succeeded" {
			wantState = "completed"
		}
		if state.Status != wantState {
			reportErr = fmt.Errorf("report status does not match task state")
		}
	}
	if !terminal {
		report = model.ReviewSnapshotReport{Available: false}
		evidence = model.ReviewSnapshotEvidence{Available: false}
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
	snapshot.Checks = snapshotChecks(run, task, state, taskErr, stateErr, reportErr, evidenceErr, snapshot.Report, snapshot.Evidence, repo, terminal)
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
	data, err := json.Marshal(snapshot)
	if err != nil {
		return model.ReviewSnapshot{}, fmt.Errorf("review snapshot encoding failed")
	}
	if s.Config.MaxReadBytes > 0 && int64(len(data)) > s.Config.MaxReadBytes {
		return model.ReviewSnapshot{}, fmt.Errorf("review snapshot exceeds configured output limit")
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

func (s *Service) readSnapshotReport(ctx context.Context, run model.Run, task model.Task) (model.ReviewSnapshotReport, error) {
	var report model.Report
	if err := s.Hub.ReadJSON(ctx, s.reportPath(run.ProjectID, run.ID), &report); err != nil {
		return model.ReviewSnapshotReport{}, err
	}
	if report.SchemaVersion != model.SchemaVersion || report.TaskID != task.ID || report.RunID != run.ID || report.ProjectID != run.ProjectID || report.Status == "" || report.FinishedAt.IsZero() {
		return model.ReviewSnapshotReport{}, fmt.Errorf("report identity or completeness mismatch")
	}
	if err := model.ValidateReport(report, task, run, s.Config.MaxListItems); err != nil {
		return model.ReviewSnapshotReport{}, err
	}
	if err := s.validateCanonicalReportProof(ctx, report, run); err != nil {
		return model.ReviewSnapshotReport{}, err
	}
	if run.Status != report.Status {
		return model.ReviewSnapshotReport{}, fmt.Errorf("report status does not match run")
	}
	commit, err := s.Hub.LastChange(ctx, s.reportPath(run.ProjectID, run.ID))
	if err != nil {
		return model.ReviewSnapshotReport{}, fmt.Errorf("report hub history unavailable")
	}
	clean := report.Repository.WorktreeClean
	return model.ReviewSnapshotReport{Available: true, Status: report.Status, Summary: report.Summary, RepositoryHead: report.Repository.Head, RepositoryBranch: report.Repository.Branch, RepositoryClean: &clean, Commits: append([]string{}, report.Repository.Commits...), ChangedFiles: append([]string{}, report.Repository.ChangedFiles...), GateResults: append([]model.CompletionGateResult{}, report.GateResults...), AcceptanceCoverage: append([]string{}, report.AcceptanceCoverage...), Deviations: append([]string{}, report.Deviations...), RemainingRisks: append([]string{}, report.RemainingRisks...), FinishedAt: &report.FinishedAt, HubCommit: commit}, nil
}

func snapshotEvidenceFromReport(report model.ReviewSnapshotReport, reportErr error) (model.ReviewSnapshotEvidence, error) {
	if reportErr != nil || !report.Available {
		return model.ReviewSnapshotEvidence{}, reportErr
	}
	clean := report.RepositoryClean
	if clean == nil {
		v := true
		clean = &v
	}
	return model.ReviewSnapshotEvidence{Available: true, Head: report.RepositoryHead, Branch: report.RepositoryBranch, WorktreeClean: clean}, nil
}

func (s *Service) fillSnapshotRepository(ctx context.Context, p config.ProjectConfig, run model.Run, evidence model.ReviewSnapshotEvidence, report model.ReviewSnapshotReport, repo *model.ReviewSnapshotRepo) {
	wt, err := s.Git.WorktreeStatus(ctx, p)
	if err != nil {
		repo.WorktreeError = snapshotDetail(err)
	} else {
		repo.Worktree = model.ReviewSnapshotWorktree{Branch: wt.Branch, Head: wt.Head, Upstream: wt.Upstream, Ahead: wt.Ahead, Behind: wt.Behind, Clean: wt.Clean}
	}
	defaultRef := "refs/heads/" + p.DefaultBranch
	defaultHead, exists, err := s.Git.ResolveMirrorRefStatus(ctx, p, defaultRef)
	if err != nil {
		repo.DefaultHeadError = snapshotDetail(err)
	} else if !exists {
		defaultHead, exists, err = s.Git.ResolveMirrorRefStatus(ctx, p, "refs/remotes/origin/"+p.DefaultBranch)
		if err != nil {
			repo.DefaultHeadError = snapshotDetail(err)
		} else if !exists {
			repo.DefaultHeadError = "default branch ref is missing"
		}
	}
	repo.DefaultHead = defaultHead
	taskHead, taskExists, err := s.Git.ResolveMirrorRefStatus(ctx, p, "refs/heads/"+run.Branch)
	if err != nil {
		repo.TaskBranchError = snapshotDetail(err)
	} else if taskExists {
		repo.TaskBranchPublished, repo.TaskBranchHead = true, taskHead
	} else {
		taskHead, taskExists, err = s.Git.ResolveMirrorRefStatus(ctx, p, "refs/remotes/origin/"+run.Branch)
		if err != nil {
			repo.TaskBranchError = snapshotDetail(err)
		} else if taskExists {
			repo.TaskBranchPublished, repo.TaskBranchHead = true, taskHead
		} else {
			repo.TaskBranchError = "task branch ref is missing"
		}
	}
	if evidence.Head != "" {
		_, evidenceExists, err := s.Git.ResolveMirrorRefStatus(ctx, p, evidence.Head)
		repo.EvidenceHeadReachable = err == nil && evidenceExists
		if err != nil {
			repo.EvidenceHeadError = snapshotDetail(err)
		} else if !evidenceExists {
			repo.EvidenceHeadError = "evidence head is missing from mirror"
		}
		if repo.EvidenceHeadReachable {
			repo.ChangedFiles, err = s.Git.MirrorChangedFiles(ctx, p, run.BaseRevision, evidence.Head)
			if err != nil {
				repo.ChangedFiles = []string{}
				repo.ChangedFilesError = snapshotDetail(err)
			}
			repo.DiffStat, err = s.Git.MirrorDiffStat(ctx, p, run.BaseRevision, evidence.Head)
			if err != nil {
				repo.DiffStatError = snapshotDetail(err)
			}
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

func snapshotChecks(run model.Run, task model.Task, state model.TaskState, taskErr, stateErr, reportErr, evidenceErr error, report model.ReviewSnapshotReport, evidence model.ReviewSnapshotEvidence, repo model.ReviewSnapshotRepo, terminal bool) []model.ReviewSnapshotCheck {
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
	if !terminal {
		add("report_identity", "critical", "not_applicable", "run is active")
		add("evidence_identity", "critical", "not_applicable", "run is active")
	} else {
		if reportErr == nil && report.Available {
			add("report_identity", "critical", "pass", "report identity and canonical hub history agree")
		} else {
			add("report_identity", "critical", "fail", nonEmpty(snapshotDetail(reportErr), "report identity or hub history is invalid"))
		}
		if evidenceErr == nil && evidence.Available {
			add("evidence_identity", "critical", "pass", "evidence identity agrees with run and task")
		} else {
			add("evidence_identity", "critical", "fail", nonEmpty(snapshotDetail(evidenceErr), "evidence identity is invalid"))
		}
	}
	if taskErr == nil && stateErr == nil && task.ID == run.TaskID && task.ProjectID == run.ProjectID && task.SHA256 == run.TaskSHA256 && task.Branch == run.Branch && state.TaskID == task.ID && state.TaskSHA256 == task.SHA256 {
		add("identity_consistency", "critical", "pass", "run, task, and task-state identities agree")
	} else {
		add("identity_consistency", "critical", "fail", snapshotDetail(firstError(taskErr, stateErr, fmt.Errorf("run, task, and task-state identities disagree"))))
	}
	if repo.RefreshSucceeded {
		add("mirror_refresh", "critical", "pass", "managed mirror refreshed once")
	} else {
		add("mirror_refresh", "critical", "fail", repo.RefreshError)
	}
	if repo.WorktreeError != "" {
		add("worktree_component", "critical", "fail", repo.WorktreeError)
	}
	if repo.DefaultHeadError != "" {
		add("default_head_component", "critical", "fail", repo.DefaultHeadError)
	}
	if repo.TaskBranchError != "" && repo.TaskBranchError != "task branch ref is missing" {
		add("task_branch_component", "critical", "fail", repo.TaskBranchError)
	}
	if repo.ChangedFilesError != "" {
		add("changed_files_component", "critical", "fail", repo.ChangedFilesError)
	}
	if repo.DiffStatError != "" {
		add("diff_stat_component", "critical", "fail", repo.DiffStatError)
	}
	if repo.EvidenceHeadReachable {
		add("evidence_head_reachable", "critical", "pass", "evidence head is reachable in mirror")
	} else if terminal {
		add("evidence_head_reachable", "critical", "fail", nonEmpty(repo.EvidenceHeadError, "evidence head is not reachable"))
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
	merged := !repo.TaskBranchPublished && repo.TaskBranchError == "task branch ref is missing" && repo.DefaultHead != "" && repo.DefaultToEvidence.Error == "" && repo.DefaultToEvidence.RightOnly == 0
	if publication || merged {
		add("branch_publication", "critical", "pass", "task branch is published or evidence is merged")
	} else if terminal {
		add("branch_publication", "critical", "fail", "task branch is unpublished and evidence is not merged")
	} else {
		add("branch_publication", "critical", "not_applicable", "run is active")
	}
	worktreeOK := repo.WorktreeError == "" && repo.Worktree.Clean && ((repo.TaskBranchPublished && repo.Worktree.Branch == run.Branch && repo.Worktree.Head == evidence.Head) || (merged && repo.Worktree.Branch == repo.DefaultBranch && repo.Worktree.Head == repo.DefaultHead && repo.DefaultToEvidence.RightOnly == 0))
	if worktreeOK {
		add("worktree_consistency", "critical", "pass", "worktree is clean and consistent")
	} else if terminal {
		add("worktree_consistency", "critical", "fail", "worktree does not match review state")
	} else {
		add("worktree_consistency", "critical", "not_applicable", "run is active")
	}
	if !terminal {
		add("changed_file_equality", "critical", "not_applicable", "run is active")
	} else if repo.ChangedFilesError == "" && report.Available && strings.Join(repo.ChangedFiles, "\x00") == strings.Join(report.ChangedFiles, "\x00") {
		add("changed_file_equality", "critical", "pass", "actual and reported changed files agree")
	} else {
		add("changed_file_equality", "critical", "fail", "actual and reported changed files differ")
	}
	if !terminal {
		add("required_gates", "critical", "not_applicable", "run is active")
	} else if report.Available {
		missing := len(report.GateResults) != len(task.RequiredGates)

		if !missing {
			for i, gate := range report.GateResults {
				if gate.ID != fmt.Sprintf("G%d", i+1) || gate.ExitCode != 0 {
					missing = true
					break
				}
			}
		}
		if !missing {
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

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func snapshotStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}
