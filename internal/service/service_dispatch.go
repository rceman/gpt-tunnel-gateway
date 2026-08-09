package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func validateTaskRunCounterIdentity(counter model.TaskRunCounter, task model.Task) error {
	if counter.ProjectID != task.ProjectID || counter.TaskID != task.ID {
		return fmt.Errorf("task run counter identity mismatch: project_id=%q task_id=%q task_project_id=%q task_id=%q", counter.ProjectID, counter.TaskID, task.ProjectID, task.ID)
	}
	return nil
}

func operationalActiveRun(run model.Run) bool {
	return !run.Historical && activeStatus(run.Status)
}

func (s *Service) checkSessionAvailable(ctx context.Context, session string) error {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		runs, err := s.RunList(ctx, project.ID)
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r.SessionKey == session && operationalActiveRun(r) {
				return fmt.Errorf("active operational run %s already owns the project session", r.ID)
			}
		}
	}
	return nil
}

func (s *Service) localRunDir(id string) string { return filepath.Join(s.Config.StateDir, "runs", id) }

func (s *Service) writeLocalRun(run model.Run, task model.Task) error {
	dir := s.localRunDir(run.ID)
	if err := fsutil.EnsureDir(dir, 0o700); err != nil {
		return err
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "run.json"), run, 0o600); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "task.json"), task, 0o600)
}

func (s *Service) TaskDispatch(ctx context.Context, in DispatchInput) (model.Run, OperationResult, error) {
	for attempt := 0; ; attempt++ {
		run, result, err := s.taskDispatchOnce(ctx, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return run, result, err
		}
	}
}

// dispatchExecutionBase resolves the immutable execution authority for a new
// run.  Existing implementation tasks may have been created before the
// canonical integration branch advanced; their published task base remains
// immutable, while the run is pinned to the refreshed branch head.  Other
// operation classes retain their prepared lineage exactly.

func (s *Service) dispatchExecutionBase(ctx context.Context, task model.Task, revision model.TaskRevision, local config.ProjectConfig) (string, error) {
	if revision.SourceRunID != "" || revision.SourceReportID != "" {
		if revision.SourceRunID == "" || revision.SourceReportID == "" || revision.BaseRevision == "" {
			return "", fmt.Errorf("correction revision is missing source lineage or reviewed base")
		}
		resolved, err := s.Git.Resolve(ctx, local.Root, revision.BaseRevision)
		if err != nil || resolved != revision.BaseRevision {
			return "", fmt.Errorf("correction reviewed base unavailable or mismatched")
		}
		return revision.BaseRevision, nil
	}
	if revision.OperationClass != "" && revision.OperationClass != "implementation" && revision.BaseRevision != "" {
		resolved, err := s.Git.Resolve(ctx, local.Root, revision.BaseRevision)
		if err != nil || resolved != revision.BaseRevision {
			return "", fmt.Errorf("task base unavailable or mismatched")
		}
		return revision.BaseRevision, nil
	}
	project, err := s.ProjectRead(ctx, task.ProjectID)
	if err != nil {
		return "", err
	}
	branch := project.DefaultBranch
	if policy, policyErr := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID); policyErr == nil && policy.IntegrationBranch != "" {
		branch = policy.IntegrationBranch
	} else if policyErr != nil && !IsNotFound(policyErr) {
		return "", policyErr
	}
	if err := s.Git.Refresh(ctx, local); err != nil {
		return "", fmt.Errorf("refresh canonical execution branch: %w", err)
	}
	head, exists, err := s.Git.MirrorBranchHead(ctx, local, branch)
	if err != nil {
		return "", fmt.Errorf("resolve canonical execution branch: %w", err)
	}
	if !exists || head == "" {
		return "", fmt.Errorf("canonical execution branch %q is unavailable", branch)
	}
	if err := model.ValidateCommitSHA(head); err != nil {
		return "", fmt.Errorf("canonical execution branch head: %w", err)
	}
	return head, nil
}
