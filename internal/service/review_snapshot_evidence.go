package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
