package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// resolveCodeDiffHead resolves a compact public 8-character fingerprint to the
// exact authoritative full commit recorded for the bound Task worktree. Only
// recorded candidate, submission, review, and integration heads are eligible;
// unknown, ambiguous, or out-of-authority fingerprints fail closed.
func (s *Service) resolveCodeDiffHead(ctx context.Context, target localCodeTarget, fingerprint8 string) (string, error) {
	if !validCodeDiffBase8(fingerprint8) || target.Kind != "task" || target.TaskID == "" {
		return "", fmt.Errorf("code diff head must be a server-authorized sha8 for a Task worktree")
	}
	state, found, stateErr := s.readExecutionForMutation(ctx, target.ProjectID, target.TaskID)
	if stateErr != nil {
		return "", stateErr
	}
	if !found || state.Worktree != target.Worktree {
		return "", fmt.Errorf("code diff head is not bound to the current Task worktree")
	}
	candidates := []string{state.BaseHead, state.Head}
	for _, stage := range []string{"code", "tests", "rebase", "integration"} {
		phases, err := s.Durability.ReadTaskExecutionPhases(ctx, target.ProjectID, target.TaskID, stage)
		if err != nil {
			return "", err
		}
		for _, phase := range phases {
			candidates = append(candidates, phase.Head)
		}
	}
	resolved := ""
	for _, candidate := range candidates {
		if model.ValidateCommitSHA(candidate) != nil || strings.ToLower(candidate[:8]) != fingerprint8 {
			continue
		}
		if resolved != "" && resolved != candidate {
			return "", fmt.Errorf("code diff head is ambiguous for this Task")
		}
		resolved = candidate
	}
	if resolved == "" {
		return "", fmt.Errorf("code diff head is not an authoritative recorded head for this Task")
	}
	ancestor, err := s.Git.IsAncestor(ctx, target.ProjectWorktree.Root, target.DiffBase, resolved)
	if err != nil {
		return "", err
	}
	if !ancestor {
		reverse, reverseErr := s.Git.IsAncestor(ctx, target.ProjectWorktree.Root, resolved, target.DiffBase)
		if reverseErr != nil {
			return "", reverseErr
		}
		if !reverse {
			return "", fmt.Errorf("code diff head is not lineage-related to the diff base")
		}
	}
	return resolved, nil
}
