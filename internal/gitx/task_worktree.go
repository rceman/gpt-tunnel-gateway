package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

var taskWorktreeSlugPattern = regexp.MustCompile(`[^a-z0-9-]+`)

// CreateTaskWorktree creates a server-owned Task lane from the exact
// mirror-authoritative default-branch commit.
func (r Runner) CreateTaskWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, taskID string, taskType model.TaskType, title, base string) (config.ProjectConfig, string, string, error) {
	path, branch, err := taskWorktreePath(stateDir, projectID, taskID, taskType, title)
	if err != nil {
		return config.ProjectConfig{}, "", "", err
	}
	if err := r.MaterializeMirrorCommit(ctx, p, p.DefaultBranch, base); err != nil {
		return config.ProjectConfig{}, "", "", err
	}
	if err := r.createTrainWorktree(ctx, p, path, branch, base); err != nil {
		return config.ProjectConfig{}, "", "", err
	}
	lane := p
	lane.Root = path
	return lane, path, branch, nil
}

// TaskReconcileConflictError reports a Task lane rebase that stopped on a
// real conflict. The lane is left mid-rebase with the conflicted bytes intact
// so the owning Agent can resolve them and continue; Target carries the exact
// server-selected canonical authority and Commit the applied commit.
type TaskReconcileConflictError struct {
	Target string
	Commit string
	Err    error
}

func (e *TaskReconcileConflictError) Error() string {
	return fmt.Sprintf("Task lane rebase conflict replaying %s onto %s: %v", e.Commit, e.Target, e.Err)
}

func (e *TaskReconcileConflictError) Unwrap() error { return e.Err }

// ReconcileTaskLane replays the divergent Task lane commits onto the
// server-selected canonical target with `git rebase --onto`. The lane must be
// clean and on its branch. A merge conflict is returned as
// TaskReconcileConflictError with the rebase left in progress for the owning
// Agent to resolve; any other failure aborts and restores the original head
// without reset --hard, force updates, or history loss.
func (r Runner) ReconcileTaskLane(ctx context.Context, p config.ProjectConfig, target, base string) (string, error) {
	if err := model.ValidateCommitSHA(target); err != nil {
		return "", err
	}
	if err := model.ValidateCommitSHA(base); err != nil {
		return "", err
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return "", err
	}
	if !status.Clean || status.Branch == "" {
		return "", fmt.Errorf("Task worktree is not a clean branch lane before reconciliation")
	}
	originalHead := status.Head
	originalBranch := status.Branch
	if _, err := r.command(ctx, p.Root, false,
		"-c", "user.name=GPT Tunnel Gateway",
		"-c", "user.email=gpt-tunnel-gateway@localhost",
		"rebase", "--empty=keep", "--reapply-cherry-picks", "--onto", target, base); err != nil {
		rebaseHead, _ := r.command(ctx, p.Root, false, "rev-parse", "--verify", "REBASE_HEAD")
		midRebase, statusErr := r.WorktreeStatus(ctx, p)
		if statusErr == nil && len(strings.TrimSpace(string(rebaseHead))) == 40 && hasUnmergedPaths(midRebase.Porcelain) {
			return "", &TaskReconcileConflictError{
				Target: target,
				Commit: strings.TrimSpace(string(rebaseHead)),
				Err:    err,
			}
		}
		_, _ = r.command(context.Background(), p.Root, false, "rebase", "--abort")
		restored, restoreErr := r.WorktreeStatus(ctx, p)
		if restoreErr != nil || restored.Head != originalHead || restored.Branch != originalBranch || !restored.Clean {
			return "", fmt.Errorf("Task lane rebase failed and lane restore is uncertain: %w", err)
		}
		return "", fmt.Errorf("Task lane rebase failed: %w", err)
	}
	head, branch, clean, err := r.CurrentHead(ctx, p)
	if err != nil || !clean || branch != originalBranch {
		return "", fmt.Errorf("reconciled Task lane is not clean on its branch")
	}
	ancestor, err := r.IsAncestor(ctx, p.Root, target, head)
	if err != nil {
		return "", err
	}
	if !ancestor {
		return "", fmt.Errorf("reconciled Task lane does not descend from refreshed canonical")
	}
	return head, nil
}

// RebaseOnto returns the in-progress rebase target (rebase-merge/onto) or an
// empty string when the lane is not mid-rebase.
func (r Runner) RebaseOnto(ctx context.Context, p config.ProjectConfig) (string, error) {
	out, err := r.command(ctx, p.Root, false, "rev-parse", "--git-path", "rebase-merge/onto")
	if err != nil {
		return "", err
	}
	rel := strings.TrimSpace(string(out))
	path := rel
	if !filepath.IsAbs(path) {
		if !filepath.IsLocal(rel) {
			return "", fmt.Errorf("invalid rebase state path")
		}
		path = filepath.Join(p.Root, rel)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	onto := strings.TrimSpace(string(content))
	if err := model.ValidateCommitSHA(onto); err != nil {
		return "", fmt.Errorf("invalid Task rebase target: %w", err)
	}
	return onto, nil
}

// BranchHead resolves the exact commit a local branch ref points at.
func (r Runner) BranchHead(ctx context.Context, p config.ProjectConfig, branch string) (string, error) {
	if err := model.ValidateBranch(branch); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Root, false, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(out))
	if err := model.ValidateCommitSHA(head); err != nil {
		return "", fmt.Errorf("invalid Task branch head: %w", err)
	}
	return head, nil
}

func hasUnmergedPaths(porcelain string) bool {
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.HasPrefix(line, "u ") {
			return true
		}
		if len(line) < 2 {
			continue
		}
		switch line[:2] {
		case "UU", "AA", "DU", "UD", "AU", "UA", "DD":
			return true
		}
	}
	return false
}
func (r Runner) RemoveTaskWorktree(ctx context.Context, p config.ProjectConfig, stateDir, projectID, taskID string, taskType model.TaskType, title, base string) error {
	path, branch, err := taskWorktreePath(stateDir, projectID, taskID, taskType, title)
	if err != nil {
		return err
	}
	lane := p
	lane.Root = path
	head, actualBranch, clean, err := r.CurrentHead(ctx, lane)
	if err != nil || !clean || actualBranch != branch || head != base {
		return fmt.Errorf("Task worktree rollback left lane untouched")
	}
	if err := r.removeTrainWorktree(ctx, p, path); err != nil {
		return err
	}
	return r.DeleteTrainBranch(ctx, p, branch, base)
}

func (r Runner) RemoveTaskWorktreeAfterIntegration(ctx context.Context, p config.ProjectConfig, stateDir, projectID, taskID string, taskType model.TaskType, title, head, branch string) error {
	path, expectedBranch, err := taskWorktreePath(stateDir, projectID, taskID, taskType, title)
	if err != nil || expectedBranch != branch {
		return fmt.Errorf("Task worktree identity is invalid")
	}
	lane := p
	lane.Root = path
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return r.DeleteTrainBranch(ctx, p, branch, head)
	} else if statErr != nil {
		return statErr
	}
	actual, actualBranch, clean, err := r.CurrentHead(ctx, lane)
	if err != nil || !clean || actualBranch != branch || actual != head {
		return fmt.Errorf("Task worktree cleanup authority is invalid")
	}
	if err := r.removeTrainWorktree(ctx, p, path); err != nil {
		return err
	}
	return r.DeleteTrainBranch(ctx, p, branch, head)
}

func taskWorktreePath(stateDir, projectID, taskID string, taskType model.TaskType, title string) (string, string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", "", err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return "", "", err
	}
	if stateDir == "" {
		return "", "", fmt.Errorf("Task worktree state directory is required")
	}
	slug := strings.ToLower(string(taskType) + "-" + title)
	slug = strings.Trim(taskWorktreeSlugPattern.ReplaceAllString(slug, "-"), "-")
	if len(slug) > 48 {
		slug = slug[:48]
	}
	if slug == "" {
		slug = "task"
	}
	branch := "task/" + taskID + "-" + slug
	path, err := TaskWorktreePath(stateDir, projectID, taskID)
	return path, branch, err
}

// TaskWorktreePath is the sole server-owned resolver for a Task lane path.
// Persisted execution records never supply this path as authority.
func TaskWorktreePath(stateDir, projectID, taskID string) (string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return "", err
	}
	if stateDir == "" {
		return "", fmt.Errorf("Task worktree state directory is required")
	}
	return filepath.Join(stateDir, "task-worktrees", projectID, taskID), nil
}
