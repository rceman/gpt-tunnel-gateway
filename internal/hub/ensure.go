package hub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

// EnsureObserver receives bounded startup sub-phase names. It is optional and
// does not alter the Hub reconciliation semantics.
type EnsureObserver func(string)

func observeEnsure(observer EnsureObserver, phase string) {
	if observer != nil {
		observer(phase)
	}
}

func (s Store) remoteRef() string {
	return "refs/remotes/" + RemoteName + "/" + s.Config.Hub.Branch
}
func (s Store) validateManagedRoot(ctx context.Context, root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed hub root must be a real directory: %s", root)
	}
	gitInfo, err := os.Stat(filepath.Join(root, ".git"))
	if err != nil || !gitInfo.IsDir() {
		return fmt.Errorf("managed hub root is not a standard Git clone: %s", root)
	}
	urlOut, err := command(ctx, root, "remote", "get-url", RemoteName)
	if err != nil {
		return err
	}
	actualURL := strings.TrimSpace(string(urlOut))
	if actualURL != s.Config.Hub.RepositoryURL {
		return fmt.Errorf("managed hub repository URL mismatch: got %q want %q", actualURL, s.Config.Hub.RepositoryURL)
	}
	status, err := command(ctx, root, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "" {
		return fmt.Errorf("managed hub worktree is dirty")
	}
	return nil
}
func (s Store) cloneIfMissing(ctx context.Context, root string) error {
	_, err := os.Lstat(root)
	if err == nil {
		return s.validateManagedRoot(ctx, root)
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(root)
	if err := fsutil.EnsureDir(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".repository-clone-")
	if err != nil {
		return err
	}
	if err := os.Remove(tmp); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := cloneRepository(ctx, parent, s.Config.Hub.RepositoryURL, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, root); err != nil {
		return fmt.Errorf("install managed hub clone: %w", err)
	}
	return s.validateManagedRoot(ctx, root)
}
func (s Store) ensureBranch(ctx context.Context, root string) error {
	exists, err := refExists(ctx, root, s.remoteRef())
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := command(ctx, root, "remote", "set-head", RemoteName, "--auto"); err != nil {
		return fmt.Errorf("resolve remote default branch: %w", err)
	}
	headRefOut, err := command(ctx, root, "symbolic-ref", "--quiet", "refs/remotes/"+RemoteName+"/HEAD")
	if err != nil {
		return fmt.Errorf("resolve remote default branch ref: %w", err)
	}
	headRef := strings.TrimSpace(string(headRefOut))
	baseOut, err := command(ctx, root, "rev-parse", "--verify", headRef+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve remote default branch commit: %w", err)
	}
	base := strings.TrimSpace(string(baseOut))
	if _, err := command(ctx, root, "push", RemoteName, base+":refs/heads/"+s.Config.Hub.Branch); err != nil {
		if _, fetchErr := command(ctx, root, "fetch", "--prune", "--tags", RemoteName); fetchErr != nil {
			return err
		}
		exists, checkErr := refExists(ctx, root, s.remoteRef())
		if checkErr != nil || !exists {
			return err
		}
		return nil
	}
	_, err = command(ctx, root, "fetch", "--prune", "--tags", RemoteName)
	return err
}
func (s Store) ensureLocked(ctx context.Context, observer EnsureObserver) (string, error) {
	root := ManagedRoot(s.Config)
	observeEnsure(observer, "HUB_ENSURE_MANAGED_ROOT_START")
	if err := s.cloneIfMissing(ctx, root); err != nil {
		return "", err
	}
	observeEnsure(observer, "HUB_ENSURE_MANAGED_ROOT_DONE")
	observeEnsure(observer, "HUB_ENSURE_REMOTE_FETCH_START")
	if _, err := command(ctx, root, "fetch", "--prune", "--tags", RemoteName); err != nil {
		return "", err
	}
	observeEnsure(observer, "HUB_ENSURE_REMOTE_FETCH_DONE")
	observeEnsure(observer, "HUB_ENSURE_BRANCH_RECONCILE_START")
	if err := s.ensureBranch(ctx, root); err != nil {
		return "", err
	}
	observeEnsure(observer, "HUB_ENSURE_BRANCH_RECONCILE_DONE")
	return root, nil
}
func (s Store) Ensure(ctx context.Context) error {
	return s.EnsureWithObserver(ctx, nil)
}
func (s Store) EnsureWithObserver(ctx context.Context, observer EnsureObserver) error {
	observeEnsure(observer, "HUB_ENSURE_LOCK_ACQUIRE_START")
	seenContention := make(map[string]struct{})
	lock, err := acquireRepositoryLockWithObserver(ctx, s.Config.StateDir, func(e lockfile.ContentionEvidence) {
		if observer == nil {
			return
		}
		key := e.BoundedJSON()
		if _, exists := seenContention[key]; exists || len(seenContention) >= 4 {
			return
		}
		seenContention[key] = struct{}{}
		observeEnsure(observer, "HUB_ENSURE_LOCK_CONTENTION "+key)
	})
	if err != nil {
		return err
	}
	defer lock.Release()
	observeEnsure(observer, "HUB_ENSURE_LOCK_ACQUIRE_DONE")
	_, err = s.ensureLocked(ctx, observer)
	if err == nil {
		observeEnsure(observer, "HUB_ENSURE_DONE")
	}
	return err
}
func (s Store) Refresh(ctx context.Context) error {
	return s.Ensure(ctx)
}
func (s Store) remoteRevisionLocked(ctx context.Context, root string) (string, error) {
	out, err := command(ctx, root, "rev-parse", "--verify", s.remoteRef()+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
func (s Store) RemoteRevision(ctx context.Context) (string, error) {
	if snapshot := readSnapshotFromContext(ctx); snapshot != nil {
		return snapshot.Revision(), nil
	}
	snapshot, err := s.FreshReadSnapshot(ctx)
	if err != nil {
		return "", err
	}
	defer snapshot.Close()
	return snapshot.Revision(), nil
}
