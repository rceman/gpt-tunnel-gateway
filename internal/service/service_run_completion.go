package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) updateRun(ctx context.Context, run model.Run, expected, subject string) (hub.TransactionResult, error) {
	if err := requireCanonicalRun(run); err != nil {
		return hub.TransactionResult{}, err
	}
	return s.Hub.Transact(ctx, expected, subject, func(w string) ([]string, error) {
		path := s.runPath(run.ProjectID, run.ID)
		if err := hub.WriteJSON(w, path, run); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
}

func taskStateStatusForResult(status string) string {
	switch status {
	case "succeeded":
		return "completed"
	default:
		return "ready"
	}
}

func canonicalReport(report model.Report) model.Report {
	report.GateResults = append([]model.CompletionGateResult{}, report.GateResults...)
	report.AcceptanceCoverage = append([]string{}, report.AcceptanceCoverage...)
	report.Deviations = append([]string{}, report.Deviations...)
	report.RemainingRisks = append([]string{}, report.RemainingRisks...)
	report.Repository.Commits = append([]string{}, report.Repository.Commits...)
	report.Repository.ChangedFiles = append([]string{}, report.Repository.ChangedFiles...)
	if report.AgentFeedback != nil {
		feedback := *report.AgentFeedback
		feedback.Friction = append([]string{}, feedback.Friction...)
		feedback.Improvements = append([]string{}, feedback.Improvements...)
		feedback.ToolCandidates = append([]model.AgentFeedbackToolCandidate{}, feedback.ToolCandidates...)
		report.AgentFeedback = &feedback
	}
	if report.GateResults == nil {
		report.GateResults = []model.CompletionGateResult{}
	}
	if report.AcceptanceCoverage == nil {
		report.AcceptanceCoverage = []string{}
	}
	return report
}

func addUniqueRisk(risks *[]string, risk string) {
	for _, existing := range *risks {
		if existing == risk {
			return
		}
	}
	*risks = append(*risks, risk)
}

func (s *Service) deriveMirrorRepositoryProof(ctx context.Context, run model.Run, project config.ProjectConfig, head string) (model.RepositoryProof, error) {
	resolved, exists, err := s.Git.ResolveMirrorRefStatus(ctx, project, head)
	if err != nil || !exists || resolved != head {
		return model.RepositoryProof{}, fmt.Errorf("durable report HEAD does not resolve exactly")
	}
	ancestor, err := s.Git.MirrorAncestor(ctx, project, run.BaseRevision, head)
	if err != nil {
		return model.RepositoryProof{}, err
	}
	files, err := s.Git.MirrorChangedFiles(ctx, project, run.BaseRevision, head)
	if err != nil {
		return model.RepositoryProof{}, err
	}
	commits, err := s.Git.MirrorLog(ctx, project, run.BaseRevision, head, s.Config.MaxListItems)
	if err != nil {
		return model.RepositoryProof{}, err
	}
	ids := make([]string, 0, len(commits))
	for _, commit := range commits {
		ids = append(ids, commit.SHA)
	}
	return model.RepositoryProof{Branch: run.Branch, Head: head, BaseAncestor: ancestor, Commits: ids, ChangedFiles: files, DiffScope: run.BaseRevision + ".." + head}, nil
}

func (s *Service) durableRepositoryProof(ctx context.Context, run model.Run, project config.ProjectConfig, localHead, localBranch string, localClean, requirePublished bool) (model.RepositoryProof, []string, error) {
	if err := s.Git.Refresh(ctx, project); err != nil {
		return model.RepositoryProof{}, nil, err
	}
	publishedHead, published, err := s.Git.MirrorBranchHead(ctx, project, run.Branch)
	if err != nil {
		return model.RepositoryProof{}, nil, err
	}
	risks := []string{}
	var proof model.RepositoryProof
	if requirePublished {
		if !published || publishedHead != localHead {
			return model.RepositoryProof{}, nil, fmt.Errorf("task branch must be pushed before finalization")
		}
		proof, err = s.deriveMirrorRepositoryProof(ctx, run, project, localHead)
		if err != nil {
			return model.RepositoryProof{}, nil, err
		}
		if !proof.BaseAncestor {
			return model.RepositoryProof{}, nil, fmt.Errorf("final project HEAD is not descended from run execution base")
		}
	} else {
		if published {
			proof, err = s.deriveMirrorRepositoryProof(ctx, run, project, publishedHead)
			if err != nil {
				return model.RepositoryProof{}, nil, err
			}
			if !proof.BaseAncestor {
				return model.RepositoryProof{}, nil, fmt.Errorf("published task branch is not descended from run execution base")
			}
		} else {
			addUniqueRisk(&risks, "published task branch was absent; canonical proof uses the run execution base")
			proof, err = s.deriveMirrorRepositoryProof(ctx, run, project, run.BaseRevision)
			if err != nil {
				return model.RepositoryProof{}, nil, err
			}
		}
	}
	proof.WorktreeClean = localClean && localBranch == run.Branch && localHead == proof.Head
	if localBranch != "" && localBranch != run.Branch {
		addUniqueRisk(&risks, "local worktree was not on the task branch; canonical proof excludes that local state")
	}
	if !localClean {
		addUniqueRisk(&risks, "local worktree was dirty; uncommitted changes were excluded from canonical proof")
	}
	if localHead != "" && localHead != proof.Head {
		addUniqueRisk(&risks, "local unpublished commits were excluded from canonical proof")
	}
	return proof, risks, nil
}

func (s *Service) failRun(ctx context.Context, run model.Run, task model.Task, status, summary, expected string) (hub.TransactionResult, error) {
	if err := requireCanonicalRun(run); err != nil {
		return hub.TransactionResult{}, err
	}
	now := time.Now().UTC()
	run.Status = status
	run.FinishedAt = &now
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return hub.TransactionResult{}, err
	}
	head := run.BaseRevision
	branch := run.Branch
	clean := false
	if local.Root != "" {
		if h, b, c, e := s.Git.CurrentHead(ctx, local); e == nil {
			head, branch, clean = h, b, c
		}
	}
	proof, risks, err := s.durableRepositoryProof(ctx, run, local, head, branch, clean, false)
	if err != nil {
		return hub.TransactionResult{}, err
	}
	report := canonicalReport(model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: task.ProjectID, Status: status, Summary: summary, RemainingRisks: risks, Repository: proof, FinishedAt: now})
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStateStatusForResult(status), UpdatedAt: now}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return hub.TransactionResult{}, err
	}
	plan.Revision++
	plan.ActiveRunID = ""
	plan.ActiveTaskID = ""
	plan.UpdatedBy = s.Config.GatewayID
	plan.UpdatedAt = now
	return s.Hub.Transact(ctx, expected, "gateway: finalize failed run "+run.ID, func(w string) ([]string, error) {
		paths := []string{s.runPath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, report, state, plan}
		for i, p := range paths {
			if err := hub.WriteJSON(w, p, vals[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
}
