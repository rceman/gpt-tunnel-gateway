package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) codeWorktreeCandidates(ctx context.Context, projectID string) ([]codeWorktreeCandidate, error) {
	return s.codeWorktreeCandidatesStream(ctx, projectID, nil)
}

func (s *Service) codeWorktreeCandidatesStream(ctx context.Context, projectID string, emit func(codeWorktreeCandidate) error) ([]codeWorktreeCandidate, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	if s.Durability == nil {
		return nil, fmt.Errorf("Shared durability unavailable")
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return nil, err
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
	candidates := make([]codeWorktreeCandidate, 0, 2)
	seen := make(map[string]struct{})
	addCandidate := func(worktree config.ProjectConfig, status gitx.WorktreeStatus, kind, label, diffBase string, createdAt time.Time, sortID string) error {
		if err := s.validateCodeSelectorIdentity(ctx, worktree, status.Head); err != nil {
			return err
		}
		var selector string
		var selectorErr error
		selector, selectorErr = codeSelector(status.Head)
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
	if err := addCandidate(project, mainStatus, "main", "main", mainStatus.Head, time.Time{}, "main"); err != nil {
		return nil, err
	}
	if err := s.codeWorktreeTaskCandidates(ctx, projectID, worktreeInventory, seen, emit, &candidates); err != nil {
		return nil, err
	}
	sortCodeWorktreeCandidates(candidates)
	return candidates, nil
}

// codeWorktreeTaskCandidates enumerates canonical Task-execution lanes from
// Shared execution state. Each state's stored worktree selector is already
// the canonical identity; the physical worktree must match the state branch.
func (s *Service) codeWorktreeTaskCandidates(ctx context.Context, projectID string, inventory gitx.WorktreeInventory, seen map[string]struct{}, emit func(codeWorktreeCandidate) error, candidates *[]codeWorktreeCandidate) error {
	if s.Durability == nil {
		return nil
	}
	states, err := s.Durability.ListTaskExecutionStates(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read Task execution states: %w", err)
	}
	for _, state := range states {
		if model.IsTaskExecutionTerminal(state.Status) {
			continue
		}
		worktree, err := inventory.Resolve("refs/heads/" + state.Branch)
		if err != nil {
			return fmt.Errorf("resolve Task %s worktree: %w", state.TaskID, err)
		}
		expectedPath, err := gitx.TaskWorktreePath(s.Config.StateDir, projectID, state.TaskID)
		if err != nil || filepath.Clean(worktree.Root) != expectedPath {
			return fmt.Errorf("Task %s worktree is not the server-owned lane", state.TaskID)
		}
		status, err := s.Git.WorktreeStatus(ctx, worktree)
		if err != nil {
			return fmt.Errorf("read Task %s worktree status: %w", state.TaskID, err)
		}
		if status.Head != state.Head {
			return fmt.Errorf("Task %s worktree head %s does not match execution state %s", state.TaskID, status.Head, state.Head)
		}
		if _, exists := seen[state.Worktree]; exists {
			return fmt.Errorf("ambiguous worktree selector %q", state.Worktree)
		}
		seen[state.Worktree] = struct{}{}
		candidate := codeWorktreeCandidate{
			localCodeTarget: localCodeTarget{
				CodeIdentity: CodeIdentity{
					ProjectID:   projectID,
					Worktree:    state.Worktree,
					Dirty:       !status.Clean,
					CurrentHead: status.Head,
				},
				ProjectWorktree: worktree,
				Kind:            "task",
				TaskID:          state.TaskID,
				DiffBase:        state.BaseHead,
			},
			Label:     state.TaskID,
			CreatedAt: state.UpdatedAt,
			SortID:    state.TaskID,
		}
		if emit != nil {
			if err := emit(candidate); err != nil {
				return err
			}
			continue
		}
		*candidates = append(*candidates, candidate)
	}
	return nil
}

func (s *Service) resolveLocalCodeTarget(ctx context.Context, projectID, selector string, live bool) (localCodeTarget, error) {
	if s.codeTargetResolver != nil {
		return s.codeTargetResolver(ctx, projectID, selector, live)
	}
	if selector == "" {
		return localCodeTarget{}, fmt.Errorf("worktree selector is required")
	}
	if strings.HasPrefix(selector, "WT-TSK") {
		return s.resolveExactTaskCodeTarget(ctx, projectID, selector, live)
	}
	if !live {
		if kind, _, prefix, parseErr := parseCodeSelector(selector); parseErr == nil && kind == "main" {
			return s.resolveCleanMainCodeTarget(ctx, projectID, selector, prefix)
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
		candidate.Live = live
		return candidate.localCodeTarget, nil
	}
	if kind, _, _, parseErr := parseCodeSelector(selector); parseErr == nil {
		for _, candidate := range candidates {
			if candidate.Kind != kind {
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
