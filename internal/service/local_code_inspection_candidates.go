package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) codeWorktreeCandidates(ctx context.Context, projectID string) ([]codeWorktreeCandidate, error) {
	return s.codeWorktreeCandidatesStream(ctx, projectID, nil)
}

type codeWorktreeCandidateSource struct {
	ProjectID  string
	Project    config.ProjectConfig
	Inventory  gitx.WorktreeInventory
	MainStatus gitx.WorktreeStatus
}

func (s *Service) codeWorktreeCandidateSource(ctx context.Context, projectID string) (codeWorktreeCandidateSource, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return codeWorktreeCandidateSource{}, err
	}
	if s.Durability == nil {
		return codeWorktreeCandidateSource{}, fmt.Errorf("Shared durability unavailable")
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return codeWorktreeCandidateSource{}, err
	}
	worktreeInventory, err := s.Git.LoadWorktreeInventory(ctx, project)
	if err != nil {
		return codeWorktreeCandidateSource{}, fmt.Errorf("read Git worktree inventory: %w", err)
	}
	canonicalMainHead, err := s.Git.RefreshDefaultBranch(ctx, project)
	if err != nil {
		return codeWorktreeCandidateSource{}, fmt.Errorf("refresh canonical main: %w", err)
	}
	mainBranch := strings.TrimPrefix(project.DefaultBranch, "refs/heads/")
	if mainBranch == "" {
		mainBranch = "main"
	}
	mainWorktree, err := worktreeInventory.Resolve("refs/heads/" + mainBranch)
	if err != nil {
		return codeWorktreeCandidateSource{}, fmt.Errorf("resolve canonical main worktree: %w", err)
	}
	project = mainWorktree
	mainStatus, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return codeWorktreeCandidateSource{}, fmt.Errorf("read main worktree status: %w", err)
	}
	if mainStatus.Branch != mainBranch {
		return codeWorktreeCandidateSource{}, fmt.Errorf("canonical main worktree is on branch %q, want %q", mainStatus.Branch, mainBranch)
	}
	if mainStatus.Head != canonicalMainHead {
		return codeWorktreeCandidateSource{}, fmt.Errorf("canonical main worktree is stale: physical head %s, canonical head %s", mainStatus.Head, canonicalMainHead)
	}
	return codeWorktreeCandidateSource{
		ProjectID:  projectID,
		Project:    project,
		Inventory:  worktreeInventory,
		MainStatus: mainStatus,
	}, nil
}

func (s *Service) newCodeWorktreeCandidate(ctx context.Context, source codeWorktreeCandidateSource, seen map[string]struct{}, worktree config.ProjectConfig, status gitx.WorktreeStatus, kind, label, diffBase string, createdAt time.Time, sortID string) (codeWorktreeCandidate, error) {
	if err := s.validateCodeSelectorIdentity(ctx, worktree, status.Head); err != nil {
		return codeWorktreeCandidate{}, err
	}
	selector, err := codeSelector(status.Head)
	if err != nil {
		return codeWorktreeCandidate{}, err
	}
	if _, exists := seen[selector]; exists {
		return codeWorktreeCandidate{}, fmt.Errorf("ambiguous worktree selector %q", selector)
	}
	seen[selector] = struct{}{}
	return codeWorktreeCandidate{
		localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				ProjectID:   source.ProjectID,
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
	}, nil
}

func (s *Service) codeWorktreeCandidatesStream(ctx context.Context, projectID string, emit func(codeWorktreeCandidate) error) ([]codeWorktreeCandidate, error) {
	source, err := s.codeWorktreeCandidateSource(ctx, projectID)
	if err != nil {
		return nil, err
	}
	candidates := make([]codeWorktreeCandidate, 0, 2)
	seen := make(map[string]struct{})
	add := func(candidate codeWorktreeCandidate) error {
		if emit != nil {
			return emit(candidate)
		}
		candidates = append(candidates, candidate)
		return nil
	}
	mainCandidate, err := s.newCodeWorktreeCandidate(ctx, source, seen, source.Project, source.MainStatus, "main", "main", source.MainStatus.Head, time.Time{}, "main")
	if err != nil {
		return nil, err
	}
	if err := add(mainCandidate); err != nil {
		return nil, err
	}
	if err := s.codeWorktreeTaskCandidates(ctx, source.ProjectID, source.Inventory, seen, emit, &candidates); err != nil {
		return nil, err
	}
	sortCodeWorktreeCandidates(candidates)
	return candidates, nil
}

func (s *Service) newCodeWorktreeTaskCandidate(ctx context.Context, projectID string, inventory gitx.WorktreeInventory, seen map[string]struct{}, state model.TaskExecutionState) (codeWorktreeCandidate, error) {
	worktree, err := inventory.Resolve("refs/heads/" + state.Branch)
	if err != nil {
		return codeWorktreeCandidate{}, fmt.Errorf("resolve Task %s worktree: %w", state.TaskID, err)
	}
	expectedPath, err := gitx.TaskWorktreePath(s.Config.StateDir, projectID, state.TaskID)
	if err != nil || filepath.Clean(worktree.Root) != expectedPath {
		return codeWorktreeCandidate{}, fmt.Errorf("Task %s worktree is not the server-owned lane", state.TaskID)
	}
	status, err := s.Git.WorktreeStatus(ctx, worktree)
	if err != nil {
		return codeWorktreeCandidate{}, fmt.Errorf("read Task %s worktree status: %w", state.TaskID, err)
	}
	if status.Head != state.Head {
		return codeWorktreeCandidate{}, fmt.Errorf("Task %s worktree head %s does not match execution state %s", state.TaskID, status.Head, state.Head)
	}
	if _, exists := seen[state.Worktree]; exists {
		return codeWorktreeCandidate{}, fmt.Errorf("ambiguous worktree selector %q", state.Worktree)
	}
	seen[state.Worktree] = struct{}{}
	return codeWorktreeCandidate{
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
	}, nil
}

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
		candidate, err := s.newCodeWorktreeTaskCandidate(ctx, projectID, inventory, seen, state)
		if err != nil {
			return err
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

func codeWorktreeCandidateMatchesQuery(candidate codeWorktreeCandidate, query string) bool {
	return query == "" || strings.Contains(candidate.CodeIdentity.Worktree, query) || strings.Contains(candidate.Label, query)
}

func (s *Service) codeWorktreeCandidatesPageStream(ctx context.Context, projectID, query string, after sqlitestore.TaskExecutionStatePageCursor, afterMain bool, emit func(codeWorktreeCandidate) error) (bool, *codeWorktreeCandidate, error) {
	source, err := s.codeWorktreeCandidateSource(ctx, projectID)
	if err != nil {
		return false, nil, err
	}
	seen := make(map[string]struct{})
	var last *codeWorktreeCandidate
	offer := func(candidate codeWorktreeCandidate) error {
		if !codeWorktreeCandidateMatchesQuery(candidate, query) {
			return nil
		}
		if err := emit(candidate); err != nil {
			return err
		}
		copy := candidate
		last = &copy
		return nil
	}
	if !afterMain && after.TaskID == "" {
		mainCandidate, candidateErr := s.newCodeWorktreeCandidate(ctx, source, seen, source.Project, source.MainStatus, "main", "main", source.MainStatus.Head, time.Time{}, "main")
		if candidateErr != nil {
			return false, nil, candidateErr
		}
		if offerErr := offer(mainCandidate); offerErr != nil {
			if errors.Is(offerErr, errCodeWorktreePageFull) {
				return true, last, nil
			}
			return false, last, offerErr
		}
	}
	for {
		page, pageErr := s.Durability.ListTaskExecutionStatesPage(ctx, projectID, query, after, sqlitestore.TaskExecutionStatePageMaxRows)
		if pageErr != nil {
			return false, last, fmt.Errorf("read Task execution state page: %w", pageErr)
		}
		for _, state := range page.States {
			if model.IsTaskExecutionTerminal(state.Status) {
				continue
			}
			candidate, candidateErr := s.newCodeWorktreeTaskCandidate(ctx, projectID, source.Inventory, seen, state)
			if candidateErr != nil {
				return false, last, candidateErr
			}
			if offerErr := offer(candidate); offerErr != nil {
				if errors.Is(offerErr, errCodeWorktreePageFull) {
					return true, last, nil
				}
				return false, last, offerErr
			}
		}
		if !page.HasMore {
			return false, last, nil
		}
		if last == nil {
			return false, nil, fmt.Errorf("Task execution state page produced no worktree candidate")
		}
		return true, last, nil
	}
}

func encodeCodeWorktreePageCursor(kind string, candidate codeWorktreeCandidate) string {
	if candidate.Kind == "main" {
		return pagination.EncodeServerCursor(kind, "main")
	}
	key := candidate.CreatedAt.UTC().Format(time.RFC3339Nano) + "\x00" + candidate.SortID + "\x00" + candidate.CodeIdentity.Worktree
	return pagination.EncodeServerCursor(kind, key)
}

func decodeCodeWorktreePageCursor(raw, kind string) (sqlitestore.TaskExecutionStatePageCursor, bool, error) {
	if raw == "" {
		return sqlitestore.TaskExecutionStatePageCursor{}, false, nil
	}
	key, ok := pagination.ResolveServerCursor(raw, kind)
	if !ok {
		return sqlitestore.TaskExecutionStatePageCursor{}, false, fmt.Errorf("invalid continuation cursor")
	}
	if key == "main" {
		return sqlitestore.TaskExecutionStatePageCursor{}, true, nil
	}
	parts := strings.Split(key, "\x00")
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return sqlitestore.TaskExecutionStatePageCursor{}, false, fmt.Errorf("invalid continuation cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil || model.ValidateCanonicalTaskID(parts[1]) != nil {
		return sqlitestore.TaskExecutionStatePageCursor{}, false, fmt.Errorf("invalid continuation cursor")
	}
	return sqlitestore.TaskExecutionStatePageCursor{UpdatedAt: parts[0], TaskID: parts[1], Worktree: parts[2]}, false, nil
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
