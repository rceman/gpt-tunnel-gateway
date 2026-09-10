package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) resolveCleanMainCodeTarget(ctx context.Context, projectID, selector, prefix string) (localCodeTarget, error) {
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	branch := strings.TrimPrefix(project.DefaultBranch, "refs/heads/")
	if branch == "" {
		branch = "main"
	}
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return localCodeTarget{}, fmt.Errorf("read canonical main worktree status: %w", err)
	}
	if status.Branch != branch {
		return localCodeTarget{}, fmt.Errorf("canonical main worktree is on branch %q, want %q", status.Branch, branch)
	}
	if !status.Clean {
		return localCodeTarget{}, fmt.Errorf("worktree selector %q is dirty; set live=true for bounded observation", selector)
	}
	if len(status.Head) < 8 || model.ValidateCommitSHA(status.Head) != nil {
		return localCodeTarget{}, fmt.Errorf("canonical main worktree has an invalid HEAD")
	}
	if !strings.HasPrefix(strings.ToLower(status.Head), prefix) {
		return localCodeTarget{}, &CodeSelectorError{
			Kind:     CodeSelectorStale,
			Selector: selector,
			Current:  "WT-MAIN-" + strings.ToLower(status.Head[:8]),
		}
	}
	if err := s.validateCodeSelectorIdentity(ctx, project, status.Head); err != nil {
		return localCodeTarget{}, err
	}
	return localCodeTarget{
		CodeIdentity: CodeIdentity{
			ProjectID:   projectID,
			Worktree:    selector,
			CurrentHead: status.Head,
		},
		ProjectWorktree: project,
		Kind:            "main",
		DiffBase:        status.Head,
	}, nil
}
