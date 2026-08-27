package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type HotfixCreateInput struct {
	Slug string `json:"slug"`
}

type HotfixCreateResult struct {
	ProjectID string `json:"project_id"`
	HotfixRef string `json:"hotfix_ref"`
	BaseSHA   string `json:"base_sha"`
	HeadSHA   string `json:"head_sha"`
}

type HotfixIntegrateInput struct {
	HotfixRef   string `json:"hotfix_ref"`
	BaseSHA     string `json:"base_sha"`
	ReviewedSHA string `json:"reviewed_sha"`
}

type HotfixIntegrateResult struct {
	ProjectID   string `json:"project_id"`
	HotfixRef   string `json:"hotfix_ref"`
	BaseSHA     string `json:"base_sha"`
	ReviewedSHA string `json:"reviewed_sha"`
	MainBefore  string `json:"main_before"`
	MainAfter   string `json:"main_after"`
}

func (s *Service) HotfixCreate(ctx context.Context, projectID string, in HotfixCreateInput) (HotfixCreateResult, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return HotfixCreateResult{}, err
	}
	if err := model.ValidateTaskSlug(in.Slug); err != nil {
		return HotfixCreateResult{}, err
	}
	p, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return HotfixCreateResult{}, err
	}
	base, err := s.Git.RefreshDefaultBranch(ctx, p)
	if err != nil {
		return HotfixCreateResult{}, err
	}
	branch := "hotfix/" + in.Slug
	worktree, err := s.Git.CreateHotfixWorktree(ctx, p, s.Config.StateDir, projectID, in.Slug, base)
	if err != nil {
		return HotfixCreateResult{}, err
	}
	head, actualBranch, clean, err := s.Git.CurrentHead(ctx, worktree)
	if err != nil {
		return HotfixCreateResult{}, err
	}
	ref := "refs/heads/" + branch
	if actualBranch != branch || !clean || head != base {
		return HotfixCreateResult{}, fmt.Errorf("created hotfix lane is not an exact clean base")
	}
	return HotfixCreateResult{ProjectID: projectID, HotfixRef: ref, BaseSHA: base, HeadSHA: head}, nil
}

func (s *Service) HotfixIntegrate(ctx context.Context, projectID string, in HotfixIntegrateInput) (HotfixIntegrateResult, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return HotfixIntegrateResult{}, err
	}
	if err := model.ValidateCommitSHA(in.BaseSHA); err != nil {
		return HotfixIntegrateResult{}, fmt.Errorf("base_sha: %w", err)
	}
	if err := model.ValidateCommitSHA(in.ReviewedSHA); err != nil {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed_sha: %w", err)
	}
	p, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
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
	baseAncestor, err := s.Git.IsAncestor(ctx, worktree.Root, in.BaseSHA, in.ReviewedSHA)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if !baseAncestor {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed hotfix is not descended from its recorded create base")
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
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed hotfix must advance origin/%s", p.DefaultBranch)
	}
	if err := s.Git.PushFastForward(ctx, p, p.DefaultBranch, mainBefore, in.ReviewedSHA); err != nil {
		return HotfixIntegrateResult{}, err
	}
	mainAfter, err := s.Git.RefreshDefaultBranch(ctx, p)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if mainAfter != in.ReviewedSHA {
		return HotfixIntegrateResult{}, fmt.Errorf("canonical origin/%s did not reach reviewed hotfix")
	}
	return HotfixIntegrateResult{ProjectID: projectID, HotfixRef: in.HotfixRef, BaseSHA: in.BaseSHA, ReviewedSHA: in.ReviewedSHA, MainBefore: mainBefore, MainAfter: mainAfter}, nil
}
