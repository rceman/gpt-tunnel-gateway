package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

type HotfixListInput struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type HotfixListItem struct {
	Task    string `json:"task"`
	Hotfix  string `json:"hotfix"`
	Head    string `json:"head"`
	Subject string `json:"subject"`
}

type HotfixReadResult struct {
	ProjectID    string `json:"project_id"`
	HotfixRef    string `json:"hotfix_ref"`
	TaskID       string `json:"task_id"`
	BaseSHA      string `json:"base_sha"`
	HeadSHA      string `json:"head_sha"`
	Materialized bool   `json:"materialized"`
}

type HotfixListResult struct {
	MainHead   string           `json:"main_head"`
	Hotfixes   []HotfixListItem `json:"hotfixes"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more,omitempty"`
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
	p, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return HotfixListResult{}, err
	}
	mainHead, exists, err := s.Git.MirrorBranchHead(ctx, p, p.DefaultBranch)
	if err != nil || !exists || model.ValidateCommitSHA(mainHead) != nil {
		return HotfixListResult{}, fmt.Errorf("canonical main head is unavailable")
	}
	identityPage, pageInfo, err := pagination.Page("hotfix_list:"+in.ProjectID, identities, limit, in.Cursor, func(identity gitx.HotfixIdentity) string { return identity.HotfixRef })
	if err != nil {
		return HotfixListResult{}, err
	}
	items := make([]HotfixListItem, 0, len(identityPage))
	for _, identity := range identityPage {
		item, itemErr := s.hotfixListItem(ctx, in.ProjectID, identity)
		if itemErr != nil {
			return HotfixListResult{}, itemErr
		}
		items = append(items, item)
	}
	return HotfixListResult{MainHead: mainHead[:8], Hotfixes: items, NextCursor: pageInfo.NextCursor, HasMore: pageInfo.HasMore}, nil
}

func (s *Service) HotfixRead(ctx context.Context, in HotfixReadInput) (HotfixReadResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return HotfixReadResult{}, err
	}
	ref, err := canonicalHotfixReadRef(in.Hotfix)
	if err != nil {
		return HotfixReadResult{}, err
	}
	identity, err := s.Git.ReadHotfixIdentity(s.Config.StateDir, in.ProjectID, ref)
	if err != nil {
		return HotfixReadResult{}, fmt.Errorf("hotfix %q: %w", in.Hotfix, err)
	}
	return s.hotfixReadResult(ctx, in.ProjectID, identity)
}

func (s *Service) resolveHotfixHead(ctx context.Context, projectID string, identity gitx.HotfixIdentity) (string, bool) {
	p, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return "", false
	}
	worktree, err := s.Git.ResolveHotfixWorktree(ctx, p, s.Config.StateDir, projectID, identity.HotfixRef)
	if err != nil {
		return "", false
	}
	head, branch, _, err := s.Git.CurrentHead(ctx, worktree)
	if err != nil || branch != strings.TrimPrefix(identity.HotfixRef, "refs/heads/") {
		return "", false
	}
	return head, true
}

func (s *Service) hotfixListItem(ctx context.Context, projectID string, identity gitx.HotfixIdentity) (HotfixListItem, error) {
	head, materialized := s.resolveHotfixHead(ctx, projectID, identity)
	item := HotfixListItem{Task: identity.TaskID, Hotfix: strings.TrimPrefix(identity.HotfixRef, "refs/heads/")}
	if !materialized {
		return item, nil
	}
	if model.ValidateCommitSHA(head) != nil {
		return HotfixListItem{}, fmt.Errorf("hotfix head is not an exact commit SHA")
	}
	item.Head = head[:8]
	p, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return HotfixListItem{}, err
	}
	commits, err := s.Git.Log(ctx, p, head, 1)
	if err == nil && len(commits) == 1 {
		item.Subject = boundedHotfixSubject(commits[0].Subject)
	}
	return item, nil
}

func boundedHotfixSubject(subject string) string {
	if !utf8.ValidString(subject) {
		subject = strings.ToValidUTF8(subject, "\uFFFD")
	}
	if len(subject) <= 160 {
		return subject
	}
	cut := subject[:160]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func (s *Service) hotfixReadResult(ctx context.Context, projectID string, identity gitx.HotfixIdentity) (HotfixReadResult, error) {
	head, materialized := s.resolveHotfixHead(ctx, projectID, identity)
	return HotfixReadResult{ProjectID: projectID, HotfixRef: identity.HotfixRef, TaskID: identity.TaskID, BaseSHA: identity.BaseSHA, HeadSHA: head, Materialized: materialized}, nil
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
