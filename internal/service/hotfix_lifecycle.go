package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type HotfixCreateInput struct {
	Slug   string `json:"slug"`
	TaskID string `json:"task"`
}

type HotfixCreateResult struct {
	ProjectID string `json:"project_id"`
	HotfixRef string `json:"hotfix_ref"`
	TaskID    string `json:"task_id"`
	BaseSHA   string `json:"base_sha"`
	HeadSHA   string `json:"head_sha"`
}

type HotfixIntegrateInput struct {
	HotfixRef   string `json:"hotfix_ref"`
	ReviewedSHA string `json:"reviewed_sha"`
}

type HotfixIntegrateResult struct {
	ProjectID   string `json:"project_id"`
	HotfixRef   string `json:"hotfix_ref"`
	TaskID      string `json:"task_id"`
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
	if err := model.ValidateCanonicalTaskID(in.TaskID); err != nil {
		return HotfixCreateResult{}, fmt.Errorf("task: %w", err)
	}
	task, err := s.TaskAuthoringFind(ctx, in.TaskID)
	if err != nil {
		return HotfixCreateResult{}, fmt.Errorf("read hotfix Task: %w", err)
	}
	if task.ProjectID != projectID {
		return HotfixCreateResult{}, fmt.Errorf("hotfix Task belongs to another project")
	}
	if task.Execution != "" {
		return HotfixCreateResult{}, fmt.Errorf("Task %q is already bound to %s execution", task.ID, task.Execution)
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
	rollback := func(cause error) (HotfixCreateResult, error) {
		if rollbackErr := s.Git.RemoveHotfixWorktree(ctx, p, s.Config.StateDir, projectID, in.Slug, base); rollbackErr != nil {
			return HotfixCreateResult{}, fmt.Errorf("%w; hotfix rollback failed: %v", cause, rollbackErr)
		}
		return HotfixCreateResult{}, cause
	}
	head, actualBranch, clean, err := s.Git.CurrentHead(ctx, worktree)
	if err != nil {
		return rollback(err)
	}
	ref := "refs/heads/" + branch
	if actualBranch != branch || !clean || head != base {
		return rollback(fmt.Errorf("created hotfix lane is not an exact clean base"))
	}
	bound, err := s.bindTaskToHotfix(ctx, task)
	if err != nil {
		return rollback(err)
	}
	if err := s.Git.RecordHotfixIdentity(s.Config.StateDir, gitx.HotfixIdentity{ProjectID: projectID, HotfixRef: ref, TaskID: bound.ID, BaseSHA: base, CreatedAt: time.Now().UTC()}); err != nil {
		return rollback(err)
	}
	return HotfixCreateResult{ProjectID: projectID, HotfixRef: ref, TaskID: bound.ID, BaseSHA: base, HeadSHA: head}, nil
}

func (s *Service) bindTaskToHotfix(ctx context.Context, task model.TaskAuthoring) (model.TaskAuthoring, error) {
	execution := model.TaskExecutionHotfix
	if s.Durability == nil {
		expected, err := s.Hub.RemoteRevision(ctx)
		if err != nil {
			return model.TaskAuthoring{}, err
		}
		updated, _, err := s.TaskAuthoringUpdate(ctx, TaskAuthoringUpdateInput{
			ProjectID: task.ProjectID, TaskID: task.ID, ExpectedRevision: task.Revision,
			ExpectedRevisionSHA256: task.RevisionSHA256, Execution: &execution, UpdatedBy: "hotfix/create",
			WriteOptions: WriteOptions{ExpectedHubRevision: expected},
		})
		return updated, err
	}
	updated, _, err := trainv2.UpdateTask(task, trainv2.AuthoringPatch{Execution: &execution}, "hotfix/create", s.durableNow())
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	digest := sha256.Sum256([]byte(task.ID + ":" + fmt.Sprint(task.Revision)))
	operationID := "hotfix-bind-" + hex.EncodeToString(digest[:])
	if _, err := s.Durability.CommitSharedMutation(ctx, sqlitestore.SharedMutation{
		OperationID: operationID, EntityType: "task", EntityID: updated.ID,
		ExpectedRevision: int64(task.Revision), Revision: int64(updated.Revision),
		Kind: "task-hotfix-bind", Payload: payload, CreatedAt: s.durableNow(),
	}); err != nil {
		return model.TaskAuthoring{}, err
	}
	return updated, nil
}

func (s *Service) HotfixIntegrate(ctx context.Context, projectID string, in HotfixIntegrateInput) (HotfixIntegrateResult, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return HotfixIntegrateResult{}, err
	}
	if err := model.ValidateCommitSHA(in.ReviewedSHA); err != nil {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed_sha: %w", err)
	}
	p, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	identity, err := s.Git.ReadHotfixIdentity(s.Config.StateDir, projectID, in.HotfixRef)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	base := identity.BaseSHA
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
	baseAncestor, err := s.Git.IsAncestor(ctx, worktree.Root, base, in.ReviewedSHA)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if !baseAncestor {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed hotfix is not descended from its recorded create base")
	}
	if in.ReviewedSHA == base {
		return HotfixIntegrateResult{}, fmt.Errorf("reviewed hotfix must advance its recorded create base")
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
		return HotfixIntegrateResult{ProjectID: projectID, HotfixRef: in.HotfixRef, TaskID: identity.TaskID, BaseSHA: base, ReviewedSHA: in.ReviewedSHA, MainBefore: mainBefore, MainAfter: mainBefore}, nil
	}
	if err := s.Git.PushFastForward(ctx, p, p.DefaultBranch, mainBefore, in.ReviewedSHA); err != nil {
		return HotfixIntegrateResult{}, err
	}
	mainAfter, err := s.Git.RefreshDefaultBranch(ctx, p)
	if err != nil {
		return HotfixIntegrateResult{}, err
	}
	if mainAfter != in.ReviewedSHA {
		return HotfixIntegrateResult{}, fmt.Errorf("canonical origin/%s did not reach reviewed hotfix", p.DefaultBranch)
	}
	return HotfixIntegrateResult{ProjectID: projectID, HotfixRef: in.HotfixRef, TaskID: identity.TaskID, BaseSHA: base, ReviewedSHA: in.ReviewedSHA, MainBefore: mainBefore, MainAfter: mainAfter}, nil
}
