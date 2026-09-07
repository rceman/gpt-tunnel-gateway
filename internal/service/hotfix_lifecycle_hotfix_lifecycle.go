package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) HotfixIntegrate(ctx context.Context, projectID string, in HotfixIntegrateInput) (HotfixIntegrateResult, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return HotfixIntegrateResult{}, err
	}
	if err := model.ValidateCommitSHA(in.ReviewedSHA); err != nil {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed_sha: %w", err)
	}
	p, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	identity, err := s.Git.ReadHotfixIdentity(s.Config.StateDir, projectID, in.HotfixRef)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	base := identity.BaseSHA
	worktree, err := s.Git.ResolveHotfixWorktree(ctx, p, s.Config.StateDir, projectID, in.HotfixRef)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, worktree)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if !clean || head != in.ReviewedSHA || branch != strings.TrimPrefix(in.HotfixRef, "refs/heads/") {
		return HotfixIntegrateResult{}, fmt.Errorf("hotfix lane is not the exact clean reviewed head")
	}
	baseAncestor, err := s.Git.IsAncestor(ctx, worktree.Root, base, in.ReviewedSHA)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if !baseAncestor {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed hotfix is not descended from its recorded create base")
	}
	if in.ReviewedSHA == base {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed hotfix must advance its recorded create base")
	}
	mainBefore, err := s.Git.RefreshDefaultBranch(ctx, p)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	mainAncestor, err := s.Git.IsAncestor(ctx, worktree.Root, mainBefore, in.ReviewedSHA)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if !mainAncestor {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed hotfix is not a strict descendant of refreshed origin/%s", p.DefaultBranch)
	}
	if mainBefore == in.ReviewedSHA {
		if _, err := s.synchronizeDefaultBranchWorktree(ctx, p, mainBefore); err != nil {
			return HotfixIntegrateResult{}, fmt.Errorf("synchronize integrated default branch worktree: %w", err)
		}
		return HotfixIntegrateResult{ProjectID: projectID, HotfixRef: in.HotfixRef, TaskID: identity.TaskID, BaseSHA: base, ReviewedSHA: in.ReviewedSHA, MainBefore: mainBefore, MainAfter: mainBefore}, nil
	}
	if err := s.Git.PushFastForward(ctx, p, p.DefaultBranch, mainBefore, in.ReviewedSHA); err != nil {
		return HotfixIntegrateResult{}, err
	}
	mainAfter, err := s.Git.RefreshDefaultBranch(ctx, p)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if mainAfter != in.ReviewedSHA {
		return HotfixIntegrateResult{}, fmt.Errorf("canonical origin/%s did not reach reviewed hotfix", p.DefaultBranch)
	}
	if _, err := s.synchronizeDefaultBranchWorktree(ctx, p, mainAfter); err != nil {
		return HotfixIntegrateResult{}, fmt.Errorf("synchronize integrated default branch worktree: %w", err)
	}
	return HotfixIntegrateResult{ProjectID: projectID, HotfixRef: in.HotfixRef, TaskID: identity.TaskID, BaseSHA: base, ReviewedSHA: in.ReviewedSHA, MainBefore: mainBefore, MainAfter: mainAfter}, nil
}
