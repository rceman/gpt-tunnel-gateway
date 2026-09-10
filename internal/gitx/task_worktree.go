package gitx

import (
	"context"
	"fmt"
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

// ReplayTaskCommits applies validated Task commits to a refreshed server-owned
// lane using the existing bounded replay primitive.
func (r Runner) ReplayTaskCommits(ctx context.Context, p config.ProjectConfig, target string, commits []string) (string, map[string]string, error) {
	head, mapping, err := r.ReplayTrainCommits(ctx, p, target, commits)
	if err != nil {
		return "", nil, fmt.Errorf("Task worktree reconciliation failed: %w", err)
	}
	return head, mapping, nil
}

// RemoveTaskWorktree removes only the lane created at the exact base by the
// current dispatch attempt; any mutation makes rollback fail closed.
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
