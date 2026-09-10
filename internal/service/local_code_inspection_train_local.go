package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) codeWorktreeCandidates(ctx context.Context, projectID string) ([]codeWorktreeCandidate, error) {
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
		candidates = append(candidates, codeWorktreeCandidate{
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
		})
		return nil
	}
	if err := addCandidate(project, mainStatus, "main", "main", "", mainStatus.Head, time.Time{}, "main"); err != nil {
		return nil, err
	}
	hotfixes, err := s.Git.ListHotfixIdentities(s.Config.StateDir, projectID)
	if err != nil {
		return nil, fmt.Errorf("read managed hotfix identities: %w", err)
	}
	for _, identity := range hotfixes {
		if identity.CreatedAt.IsZero() {
			continue
		}
		worktree, resolveErr := s.Git.ResolveHotfixWorktreeFromInventory(worktreeInventory, s.Config.StateDir, projectID, identity.HotfixRef)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve managed hotfix %s worktree: %w", identity.HotfixRef, resolveErr)
		}
		status, statusErr := s.Git.WorktreeStatus(ctx, worktree)
		if statusErr != nil {
			return nil, fmt.Errorf("read managed hotfix %s worktree status: %w", identity.HotfixRef, statusErr)
		}
		slug := strings.TrimPrefix(identity.HotfixRef, "refs/heads/hotfix/")
		if err := addCandidate(worktree, status, "hotfix", slug, "", identity.BaseSHA, identity.CreatedAt, slug); err != nil {
			return nil, err
		}
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

func codeTrainBase(train model.TrainV2, currentHead string) (string, error) {
	if len(train.Items) == 0 || len(train.Items[0].Attempts) == 0 {
		if train.Status == model.TrainV2Planned {
			return currentHead, nil
		}
		return "", fmt.Errorf("managed Train %s has no canonical Shared Train base", train.ID)
	}
	// The first Attempt of the first Shared Train item is the canonical lane
	// base. Later item Attempts start from intermediate heads and must not be
	// mistaken for separate Train bases.
	base := train.Items[0].Attempts[0].StartHead
	if model.ValidateCommitSHA(base) != nil {
		return "", fmt.Errorf("managed Train %s has an invalid canonical Shared Train base", train.ID)
	}
	return base, nil
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

func candidateTrainNumber(trainID string) uint64 {
	_, number, _ := model.ParseTrainV2ID(trainID)
	return number
}

func parseCodeSelector(selector string) (string, uint64, string, error) {
	if strings.HasPrefix(selector, "WT-MAIN-") && len(selector) == len("WT-MAIN-")+8 {
		prefix := selector[len("WT-MAIN-"):]
		if !validSelectorPrefix(prefix) {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		return "main", 0, prefix, nil
	}
	if strings.HasPrefix(selector, "WT-FIX-") {
		rest := strings.TrimPrefix(selector, "WT-FIX-")
		separator := strings.LastIndexByte(rest, '-')
		if separator < 1 || separator == len(rest)-1 {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		slug, prefix := rest[:separator], rest[separator+1:]
		if model.ValidateTaskSlug(slug) != nil || !validSelectorPrefix(prefix) {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		return "hotfix", 0, slug, nil
	}
	if !strings.HasPrefix(selector, "WT-TRN") {
		return "", 0, "", fmt.Errorf("invalid worktree selector")
	}
	parts := strings.Split(strings.TrimPrefix(selector, "WT-TRN"), "-")
	if len(parts) != 2 || len(parts[1]) != 8 || (len(parts[0]) > 1 && strings.HasPrefix(parts[0], "0")) || !validSelectorPrefix(parts[1]) {
		return "", 0, "", fmt.Errorf("invalid worktree selector")
	}
	number, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return "", 0, "", err
	}
	return "train", number, parts[1], nil
}

func validSelectorPrefix(prefix string) bool {
	if len(prefix) != 8 {
		return false
	}
	for _, char := range prefix {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}
