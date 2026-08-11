package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
		gateResults := report.GateResults
		serverGates := len(report.ServerGateResults) > 0
		missing := len(gateResults) != len(task.RequiredGates)
		if serverGates {
			gateResults = report.ServerGateResults
			missing = false
		}

		if !missing {
			for i, gate := range gateResults {
				if (!serverGates && gate.ID != fmt.Sprintf("G%d", i+1)) || gate.ExitCode != 0 {
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
