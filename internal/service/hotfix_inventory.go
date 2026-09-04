package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

type HotfixListInput struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type HotfixInventoryItem struct {
	ProjectID string `json:"project_id"`
	HotfixRef string `json:"hotfix_ref"`
	TaskID    string `json:"task_id"`
	BaseSHA   string `json:"base_sha"`
	HeadSHA   string `json:"head_sha,omitempty"`
	State     string `json:"state"`
}

type HotfixListResult struct {
	Hotfixes   []HotfixInventoryItem `json:"hotfixes"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more,omitempty"`
}

type HotfixReadInput struct {
	ProjectID string `json:"project_id"`
	Hotfix    string `json:"hotfix"`
}

func (s *Service) HotfixList(ctx context.Context, in HotfixListInput) (HotfixListResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return HotfixListResult{}, err
	}
	limit, err := pagination.Limit(in.Limit, MaxPublicCollectionLimit)
	if err != nil {
		return HotfixListResult{}, err
	}
	identities, err := s.Git.ListHotfixIdentities(s.Config.StateDir, in.ProjectID)
	if err != nil {
		return HotfixListResult{}, err
	}
	sort.SliceStable(identities, func(i, j int) bool {
		if !identities[i].CreatedAt.Equal(identities[j].CreatedAt) {
			return identities[i].CreatedAt.After(identities[j].CreatedAt)
		}
		return identities[i].HotfixRef > identities[j].HotfixRef
	})
	items := make([]HotfixInventoryItem, 0, len(identities))
	for _, identity := range identities {
		item, itemErr := s.hotfixInventoryItem(ctx, in.ProjectID, identity)
		if itemErr != nil {
			return HotfixListResult{}, itemErr
		}
		items = append(items, item)
	}
	page, pageInfo, err := pagination.Page("hotfix_list:"+in.ProjectID, items, limit, in.Cursor, func(item HotfixInventoryItem) string { return item.HotfixRef })
	if err != nil {
		return HotfixListResult{}, err
	}
	return HotfixListResult{Hotfixes: page, NextCursor: pageInfo.NextCursor, HasMore: pageInfo.HasMore}, nil
}

func (s *Service) HotfixRead(ctx context.Context, in HotfixReadInput) (HotfixInventoryItem, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return HotfixInventoryItem{}, err
	}
	ref, err := canonicalHotfixReadRef(in.Hotfix)
	if err != nil {
		return HotfixInventoryItem{}, err
	}
	identity, err := s.Git.ReadHotfixIdentity(s.Config.StateDir, in.ProjectID, ref)
	if err != nil {
		return HotfixInventoryItem{}, fmt.Errorf("hotfix %q: %w", in.Hotfix, err)
	}
	return s.hotfixInventoryItem(ctx, in.ProjectID, identity)
}

func (s *Service) hotfixInventoryItem(ctx context.Context, projectID string, identity gitx.HotfixIdentity) (HotfixInventoryItem, error) {
	item := HotfixInventoryItem{ProjectID: projectID, HotfixRef: identity.HotfixRef, TaskID: identity.TaskID, BaseSHA: identity.BaseSHA, State: "unmaterialized"}
	p, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return HotfixInventoryItem{}, err
	}
	worktree, err := s.Git.ResolveHotfixWorktree(ctx, p, s.Config.StateDir, projectID, identity.HotfixRef)
	if err != nil {
		return item, nil
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, worktree)
	if err != nil || branch != strings.TrimPrefix(identity.HotfixRef, "refs/heads/") {
		return item, nil
	}
	item.HeadSHA = head
	item.State = "clean"
	if !clean {
		item.State = "dirty"
	}
	return item, nil
}

func canonicalHotfixReadRef(value string) (string, error) {
	value = strings.TrimPrefix(value, "refs/heads/")
	if !strings.HasPrefix(value, "hotfix/") {
		return "", fmt.Errorf("hotfix must be a canonical hotfix/<slug> reference")
	}
	if err := model.ValidateTaskSlug(strings.TrimPrefix(value, "hotfix/")); err != nil {
		return "", err
	}
	return "refs/heads/" + value, nil
}
