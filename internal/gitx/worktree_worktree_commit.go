package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// CommitCandidate stages the server-verified candidate tree and creates its
// checkpoint commit. The caller must perform all gate and drift checks first.
func (r Runner) CommitCandidate(ctx context.Context, p config.ProjectConfig, message string) (string, error) {
	if strings.TrimSpace(message) == "" || strings.ContainsAny(message, "\x00\r\n") {
		return "", fmt.Errorf("invalid candidate commit message")
	}
	if _, err := r.command(ctx, p.Root, false, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	if _, err := r.command(ctx, p.Root, false, "-c", "user.name=GPT Tunnel Gateway", "-c", "user.email=gpt-tunnel-gateway@localhost", "commit", "-m", message); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Root, false, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(out))
	if err := model.ValidateCommitSHA(head); err != nil {
		return "", fmt.Errorf("invalid candidate commit: %w", err)
	}
	return head, nil
}

// TreeID returns the exact Git content tree for the current HEAD. Commit
// identity is intentionally not part of this value so equivalent trees can
// be recognized across commits.
func (r Runner) TreeID(ctx context.Context, p config.ProjectConfig) (string, error) {
	out, err := r.command(ctx, p.Root, false, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if err := model.ValidateRevision(tree); err != nil {
		return "", fmt.Errorf("invalid Git tree identity: %w", err)
	}
	return tree, nil
}

// WorktreeContentID returns the exact prospective Git tree object for the
// current worktree by staging into a private temporary index. The repository's
// real index is never changed.
func (r Runner) WorktreeContentID(ctx context.Context, p config.ProjectConfig) (string, error) {
	tempDir, err := os.MkdirTemp("", "gpt-tunnel-test-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	indexPath := filepath.Join(tempDir, "index")
	env := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err := r.commandWithEnv(ctx, p.Root, false, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := r.commandWithEnv(ctx, p.Root, false, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	out, err := r.commandWithEnv(ctx, p.Root, false, env, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if err := model.ValidateRevision(tree); err != nil {
		return "", fmt.Errorf("invalid prospective Git tree identity: %w", err)
	}
	return tree, nil
}
