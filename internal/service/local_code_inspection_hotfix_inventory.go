package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
)

func (s *Service) codeWorktreeHotfixCandidates(ctx context.Context, projectID string, inventory gitx.WorktreeInventory, add func(config.ProjectConfig, gitx.WorktreeStatus, string, string, string, string, time.Time, string) error) error {
	cursor := ""
	for {
		identities, info, err := s.Git.ListHotfixIdentitiesPage(s.Config.StateDir, projectID, MaxPublicCollectionLimit, cursor)
		if err != nil {
			return fmt.Errorf("read managed hotfix identities: %w", err)
		}
		for _, identity := range identities {
			if identity.CreatedAt.IsZero() {
				continue
			}
			worktree, err := s.Git.ResolveHotfixWorktreeFromInventory(inventory, s.Config.StateDir, projectID, identity.HotfixRef)
			if err != nil {
				return fmt.Errorf("resolve managed hotfix %s worktree: %w", identity.HotfixRef, err)
			}
			status, err := s.Git.WorktreeStatus(ctx, worktree)
			if err != nil {
				return fmt.Errorf("read managed hotfix %s worktree status: %w", identity.HotfixRef, err)
			}
			slug := strings.TrimPrefix(identity.HotfixRef, "refs/heads/hotfix/")
			if err := add(worktree, status, "hotfix", slug, "", identity.BaseSHA, identity.CreatedAt, slug); err != nil {
				return err
			}
		}
		if !info.HasMore {
			return nil
		}
		if info.NextCursor == "" {
			return fmt.Errorf("empty hotfix identity continuation")
		}
		cursor = info.NextCursor
	}
}
