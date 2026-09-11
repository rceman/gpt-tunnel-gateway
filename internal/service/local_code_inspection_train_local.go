package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) codeWorktreeCandidates(ctx context.Context, projectID string) ([]codeWorktreeCandidate, error) {
	return s.codeWorktreeCandidatesStream(ctx, projectID, nil)
}

func (s *Service) codeWorktreeCandidatesStream(ctx context.Context, projectID string, emit func(codeWorktreeCandidate) error) ([]codeWorktreeCandidate, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return nil, err
	}
	trains, err := s.codeTrainRecords(ctx, projectID)
	if err != nil && !IsNotFound(err) {
		return nil, fmt.Errorf("read managed Train worktrees: %w", err)
	}
	worktreeInventory, err := s.Git.LoadWorktreeInventory(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("read Git worktree inventory: %w", err)
	}
	canonicalMainHead, err := s.Git.RefreshDefaultBranch(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("refresh canonical main: %w", err)
	}
	mainBranch := strings.TrimPrefix(project.DefaultBranch, "refs/heads/")
	if mainBranch == "" {
		mainBranch = "main"
	}
	mainWorktree, err := worktreeInventory.Resolve("refs/heads/" + mainBranch)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical main worktree: %w", err)
	}
	project = mainWorktree
	mainStatus, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("read main worktree status: %w", err)
	}
	if mainStatus.Branch != mainBranch {
		return nil, fmt.Errorf("canonical main worktree is on branch %q, want %q", mainStatus.Branch, mainBranch)
	}
	if mainStatus.Head != canonicalMainHead {
		return nil, fmt.Errorf("canonical main worktree is stale: physical head %s, canonical head %s", mainStatus.Head, canonicalMainHead)
	}
	mainHead := mainStatus.Head
	managed := make(map[string]model.TrainV2, len(trains))
	for _, train := range trains {
		if train.ProjectID == projectID && activeCodeTrainStatus(train.Status) && train.Historical == nil {
			managed[train.ID] = train
		}
	}
	candidates := make([]codeWorktreeCandidate, 0, len(managed)+1)
	seen := make(map[string]struct{})
	addCandidate := func(worktree config.ProjectConfig, status gitx.WorktreeStatus, kind, label, trainID, diffBase string, createdAt time.Time, sortID string) error {
		if err := s.validateCodeSelectorIdentity(ctx, worktree, status.Head); err != nil {
			return err
		}
		var selector string
		var selectorErr error
		if kind == "hotfix" {
			selector, selectorErr = codeHotfixSelector(label, status.Head)
		} else {
			selector, selectorErr = codeSelector(trainID, status.Head)
		}
		if selectorErr != nil {
			return selectorErr
		}
		if _, exists := seen[selector]; exists {
			return fmt.Errorf("ambiguous worktree selector %q", selector)
		}
		seen[selector] = struct{}{}
		candidate := codeWorktreeCandidate{
			localCodeTarget: localCodeTarget{
				CodeIdentity: CodeIdentity{
					ProjectID:   projectID,
					Worktree:    selector,
					Dirty:       !status.Clean,
					CurrentHead: status.Head,
				},
				ProjectWorktree: worktree,
				Kind:            kind,
				TrainID:         trainID,
				DiffBase:        diffBase,
			},
			Label:     label,
			CreatedAt: createdAt,
			SortID:    sortID,
		}
		if emit != nil {
			return emit(candidate)
		}
		candidates = append(candidates, candidate)
		return nil
	}
	if err := addCandidate(project, mainStatus, "main", "main", "", mainStatus.Head, time.Time{}, "main"); err != nil {
		return nil, err
	}
	if err := s.codeWorktreeHotfixCandidates(ctx, projectID, worktreeInventory, addCandidate); err != nil {
		return nil, err
	}
	trainIDs := make([]string, 0, len(managed))
	for candidateID := range managed {
		trainIDs = append(trainIDs, candidateID)
	}
	sort.Strings(trainIDs)
	for _, candidateID := range trainIDs {
		train := managed[candidateID]
		runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, projectID, candidateID)
		if runtimeErr != nil {
			return nil, fmt.Errorf("read managed Train %s runtime: %w", candidateID, runtimeErr)
		}
		runtimeBinding := &runtime
		expectedPath, pathErr := codeTrainWorktreePath(s.Config.StateDir, projectID, project, candidateID, runtimeBinding)
		if pathErr != nil {
			return nil, pathErr
		}
		worktree, resolveErr := worktreeInventory.Resolve("refs/heads/train/" + candidateID)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve managed Train %s worktree: %w", candidateID, resolveErr)
		}
		if filepath.Clean(worktree.Root) != filepath.Clean(expectedPath) {
			return nil, fmt.Errorf("managed Train %s is bound to unexpected worktree path", candidateID)
		}
		status, statusErr := s.Git.WorktreeStatus(ctx, worktree)
		if statusErr != nil {
			return nil, fmt.Errorf("read managed Train %s worktree status: %w", candidateID, statusErr)
		}
		merged, ancestorErr := s.Git.IsAncestor(ctx, project.Root, status.Head, mainHead)
		if ancestorErr != nil {
			return nil, fmt.Errorf("check Train %s merge state: %w", candidateID, ancestorErr)
		}
		if merged {
			continue
		}
		base, baseErr := codeTrainBase(train, status.Head)
		if baseErr != nil {
			return nil, baseErr
		}
		if base != status.Head {
			ancestor, ancestorErr := s.Git.IsAncestor(ctx, worktree.Root, base, status.Head)
			if ancestorErr != nil || !ancestor {
				return nil, fmt.Errorf("managed Train %s has an invalid authoritative base", candidateID)
			}
		}
		if err := addCandidate(worktree, status, "train", candidateID, candidateID, base, train.CreatedAt, candidateID); err != nil {
			return nil, err
		}
	}
	sortCodeWorktreeCandidates(candidates)
	return candidates, nil
}

func (s *Service) resolveLocalCodeTarget(ctx context.Context, projectID, selector string, live bool) (localCodeTarget, error) {
	if s.codeTargetResolver != nil {
		return s.codeTargetResolver(ctx, projectID, selector, live)
	}
	if selector == "" {
		return localCodeTarget{}, fmt.Errorf("worktree selector is required")
	}
	if !live {
		if kind, _, prefix, parseErr := parseCodeSelector(selector); parseErr == nil && kind == "main" {
			return s.resolveCleanMainCodeTarget(ctx, projectID, selector, prefix)
		}
		if kind, _, slug, parseErr := parseCodeSelector(selector); parseErr == nil && kind == "hotfix" {
			return s.resolveExactHotfixCodeTarget(ctx, projectID, selector, slug, live)
		}
	}
	if live {
		if kind, _, slug, parseErr := parseCodeSelector(selector); parseErr == nil && kind == "hotfix" {
			return s.resolveExactHotfixCodeTarget(ctx, projectID, selector, slug, live)
		}
	}
	candidates, err := s.codeWorktreeCandidates(ctx, projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	for _, candidate := range candidates {
		if candidate.CodeIdentity.Worktree != selector {
			continue
		}
		if !live && candidate.Dirty {
			return localCodeTarget{}, fmt.Errorf("worktree selector %q is dirty; set live=true for bounded observation", selector)
		}
		if (candidate.Kind == "train" || candidate.Kind == "hotfix") && candidate.DiffBase == "" {
			return localCodeTarget{}, fmt.Errorf("worktree selector %q has no authoritative Train base", selector)
		}
		candidate.Live = live
		if candidate.Kind == "train" || candidate.Kind == "hotfix" {
			ancestor, ancestorErr := s.Git.IsAncestor(ctx, candidate.ProjectWorktree.Root, candidate.DiffBase, candidate.CurrentHead)
			if ancestorErr != nil || !ancestor {
				return localCodeTarget{}, fmt.Errorf("worktree selector %q has an invalid authoritative Train base", selector)
			}
		}
		return candidate.localCodeTarget, nil
	}
	if kind, number, prefix, parseErr := parseCodeSelector(selector); parseErr == nil {
		for _, candidate := range candidates {
			if candidate.Kind != kind || (kind == "train" && number != candidateTrainNumber(candidate.TrainID)) || (kind == "hotfix" && candidate.SortID != prefix) {
				continue
			}
			return localCodeTarget{}, &CodeSelectorError{
				Kind:     CodeSelectorStale,
				Selector: selector,
				Current:  candidate.CodeIdentity.Worktree,
			}
		}
	}
	return localCodeTarget{}, &CodeSelectorError{
		Kind:     CodeSelectorNotFound,
		Selector: selector,
	}
}

func (s *Service) resolveExactHotfixCodeTargetDetached(ctx context.Context, projectID, selector, slug string, live bool) (localCodeTarget, error) {
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	ref := "refs/heads/hotfix/" + slug
	identity, err := s.Git.ReadHotfixIdentity(s.Config.StateDir, projectID, ref)
	if err != nil {
		return localCodeTarget{}, &CodeSelectorError{
			Kind:     CodeSelectorNotFound,
			Selector: selector,
		}
	}
	worktree, err := s.Git.ResolveHotfixWorktree(ctx, project, s.Config.StateDir, projectID, identity.HotfixRef)
	if err != nil {
		return localCodeTarget{}, fmt.Errorf("resolve managed hotfix %s worktree: %w", identity.HotfixRef, err)
	}
	status, err := s.Git.WorktreeStatus(ctx, worktree)
	if err != nil {
		return localCodeTarget{}, fmt.Errorf("read managed hotfix %s worktree status: %w", identity.HotfixRef, err)
	}
	currentSelector, err := codeHotfixSelector(slug, status.Head)
	if err != nil {
		return localCodeTarget{}, err
	}
	if currentSelector != selector {
		return localCodeTarget{}, &CodeSelectorError{
			Kind:     CodeSelectorStale,
			Selector: selector,
			Current:  currentSelector,
		}
	}
	if !live && !status.Clean {
		return localCodeTarget{}, fmt.Errorf("worktree selector %q is dirty; set live=true for bounded observation", selector)
	}
	ancestor, err := s.Git.IsAncestor(ctx, worktree.Root, identity.BaseSHA, status.Head)
	if err != nil || !ancestor {
		return localCodeTarget{}, fmt.Errorf("worktree selector %q has an invalid authoritative hotfix base", selector)
	}
	return localCodeTarget{
		CodeIdentity: CodeIdentity{
			ProjectID:   projectID,
			Worktree:    selector,
			Dirty:       !status.Clean,
			CurrentHead: status.Head,
			Live:        live,
		},
		ProjectWorktree: worktree,
		Kind:            "hotfix",
		DiffBase:        identity.BaseSHA,
	}, nil
}
