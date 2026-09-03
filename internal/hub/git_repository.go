package hub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const hubCommandOutputLimit int64 = 64 << 20

func cleanEnv(extra ...string) []string {
	keys := []string{"HOME", "PATH", "SSH_AUTH_SOCK", "USER", "LOGNAME", "TMPDIR"}
	out := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return append(out, extra...)
}
func command(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = cleanEnv()
	stdout := boundedCommandBuffer{limit: hubCommandOutputLimit}
	stderr := boundedCommandBuffer{limit: hubCommandOutputLimit}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		if stdout.exceeded || stderr.exceeded {
			return nil, fmt.Errorf("git %s output exceeds %d bytes", strings.Join(args, " "), hubCommandOutputLimit)
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("git %s output exceeds %d bytes", strings.Join(args, " "), hubCommandOutputLimit)
	}
	return stdout.data, nil
}
func cloneRepository(ctx context.Context, parent, repositoryURL, target string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--origin", RemoteName, "--", repositoryURL, target)
	cmd.Dir = parent
	cmd.Env = cleanEnv()
	stdout := boundedCommandBuffer{limit: hubCommandOutputLimit}
	stderr := boundedCommandBuffer{limit: hubCommandOutputLimit}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("clone managed hub repository: %w", ctxErr)
		}
		if stdout.exceeded || stderr.exceeded {
			return fmt.Errorf("clone managed hub repository output exceeds %d bytes", hubCommandOutputLimit)
		}
		return fmt.Errorf("clone managed hub repository: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded || stderr.exceeded {
		return fmt.Errorf("clone managed hub repository output exceeds %d bytes", hubCommandOutputLimit)
	}
	return nil
}
func refExists(ctx context.Context, root, ref string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = root
	cmd.Env = cleanEnv()
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("inspect ref %s: %w", ref, err)
	}
	return true, nil
}

type boundedCommandBuffer struct {
	data     []byte
	limit    int64
	exceeded bool
}

func (b *boundedCommandBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - int64(len(b.data))
	if remaining > 0 {
		if int64(n) > remaining {
			p = p[:remaining]
			b.exceeded = true
		}
		b.data = append(b.data, p...)
	} else if n > 0 {
		b.exceeded = true
	}
	return n, nil
}

func (b *boundedCommandBuffer) String() string { return string(b.data) }
func acquireRepositoryLock(ctx context.Context, stateDir string) (*lockfile.Lock, error) {
	return acquireRepositoryLockWithObserver(ctx, stateDir, nil)
}

func acquireRepositoryLockWithObserver(ctx context.Context, stateDir string, onBusy func(lockfile.ContentionEvidence)) (*lockfile.Lock, error) {
	path := filepath.Join(stateDir, "locks", "hub-repository.lock")
	for {
		lock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "hub-repository")
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		if onBusy != nil {
			onBusy(lockfile.ReadContentionEvidence(path))
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("hub-repository flock acquisition: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
