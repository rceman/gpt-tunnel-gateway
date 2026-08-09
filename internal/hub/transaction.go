package hub

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

func (s Store) Transact(ctx context.Context, expected, subject string, mutate Mutator) (TransactionResult, error) {
	transactionLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "hub")
	if err != nil {
		return TransactionResult{}, err
	}
	defer transactionLock.Release()
	repositoryLock, err := acquireRepositoryLock(ctx, s.Config.StateDir)
	if err != nil {
		return TransactionResult{}, err
	}
	defer repositoryLock.Release()
	root, err := s.ensureLocked(ctx)
	if err != nil {
		return TransactionResult{}, err
	}
	statusOut, err := command(ctx, root, "status", "--porcelain")
	if err != nil {
		return TransactionResult{}, err
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		return TransactionResult{}, fmt.Errorf("managed hub worktree is dirty")
	}
	before, err := s.remoteRevisionLocked(ctx, root)
	if err != nil {
		return TransactionResult{}, err
	}
	if expected != "" && expected != before {
		return TransactionResult{}, fmt.Errorf("HUB_REVISION_CONFLICT expected=%s actual=%s", expected, before)
	}
	base := filepath.Join(s.Config.StateDir, "hub-worktrees")
	if err := fsutil.EnsureDir(base, 0o700); err != nil {
		return TransactionResult{}, err
	}
	worktree, err := os.MkdirTemp(base, "tx-")
	if err != nil {
		return TransactionResult{}, err
	}
	_ = os.Remove(worktree)
	defer os.RemoveAll(worktree)
	if _, err = command(ctx, root, "worktree", "add", "--detach", worktree, before); err != nil {
		return TransactionResult{}, err
	}
	defer command(context.Background(), root, "worktree", "remove", "--force", worktree)
	paths, err := mutate(worktree)
	if err != nil {
		return TransactionResult{}, err
	}
	if len(paths) == 0 {
		return TransactionResult{}, fmt.Errorf("transaction produced no paths")
	}
	for _, path := range paths {
		if err := validateHubPath(path); err != nil {
			return TransactionResult{}, err
		}
	}
	args := append([]string{"add", "--"}, paths...)
	if _, err = command(ctx, worktree, args...); err != nil {
		return TransactionResult{}, err
	}
	diffCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	diffCmd.Dir = worktree
	diffCmd.Env = cleanEnv()
	diffErr := diffCmd.Run()
	if diffErr == nil {
		return TransactionResult{}, fmt.Errorf("transaction produced no changes")
	}
	if exit, ok := diffErr.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		return TransactionResult{}, fmt.Errorf("inspect staged transaction: %w", diffErr)
	}
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", subject)
	commitCmd.Dir = worktree
	commitCmd.Env = cleanEnv("GIT_AUTHOR_NAME="+s.Config.Hub.AuthorName, "GIT_AUTHOR_EMAIL="+s.Config.Hub.AuthorEmail, "GIT_COMMITTER_NAME="+s.Config.Hub.AuthorName, "GIT_COMMITTER_EMAIL="+s.Config.Hub.AuthorEmail)
	var stderr bytes.Buffer
	commitCmd.Stderr = &stderr
	if err := commitCmd.Run(); err != nil {
		return TransactionResult{}, fmt.Errorf("git commit: %w: %s", err, stderr.String())
	}
	afterOut, err := command(ctx, worktree, "rev-parse", "HEAD")
	if err != nil {
		return TransactionResult{}, err
	}
	after := strings.TrimSpace(string(afterOut))
	if _, err = command(ctx, worktree, "push", RemoteName, "HEAD:refs/heads/"+s.Config.Hub.Branch); err != nil {
		return TransactionResult{}, err
	}
	remoteOut, err := command(ctx, root, "ls-remote", RemoteName, "refs/heads/"+s.Config.Hub.Branch)
	if err != nil {
		return TransactionResult{}, err
	}
	fields := strings.Fields(string(remoteOut))
	if len(fields) < 1 || fields[0] != after {
		return TransactionResult{}, fmt.Errorf("remote verification failed: got %q want %q", strings.TrimSpace(string(remoteOut)), after)
	}
	return TransactionResult{
		Before: before,
		After:  after,
		Remote: RemoteName,
		Branch: s.Config.Hub.Branch,
		Paths:  append([]string{}, paths...),
	}, nil
}
