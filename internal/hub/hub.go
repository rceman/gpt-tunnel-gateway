package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const (
	ProtocolRoot = "gpt-tunnel/v1"
	RemoteName   = "origin"
)

type Store struct {
	Config config.Config
}

type TransactionResult struct {
	Before string   `json:"before"`
	After  string   `json:"after"`
	Remote string   `json:"remote"`
	Branch string   `json:"branch"`
	Paths  []string `json:"paths"`
}

type Mutator func(worktree string) ([]string, error)

func ManagedRoot(c config.Config) string {
	return filepath.Join(c.StateDir, "hub", "repository")
}

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
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
func cloneRepository(ctx context.Context, parent, repositoryURL, target string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--origin", RemoteName, "--", repositoryURL, target)
	cmd.Dir = parent
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone managed hub repository: %w: %s", err, strings.TrimSpace(stderr.String()))
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
func acquireRepositoryLock(ctx context.Context, stateDir string) (*lockfile.Lock, error) {
	for {
		lock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "hub-repository")
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
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
func (s Store) ensureLocked(ctx context.Context) (string, error) {
	root := ManagedRoot(s.Config)
	if err := s.cloneIfMissing(ctx, root); err != nil {
		return "", err
	}
	if _, err := command(ctx, root, "fetch", "--prune", "--tags", RemoteName); err != nil {
		return "", err
	}
	if err := s.ensureBranch(ctx, root); err != nil {
		return "", err
	}
	return root, nil
}
func (s Store) Ensure(ctx context.Context) error {
	lock, err := acquireRepositoryLock(ctx, s.Config.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	_, err = s.ensureLocked(ctx)
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
	lock, err := s.readOnlyLock()
	if err != nil {
		return "", err
	}
	defer lock.Release()
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		return "", err
	}
	return s.remoteRevisionLocked(ctx, root)
}

func (s Store) readOnlyLock() (*lockfile.Lock, error) {
	return lockfile.AcquireReadOnly(filepath.Join(s.Config.StateDir, "locks"), "hub-repository")
}

func (s Store) readOnlyRoot(ctx context.Context) (string, error) {
	root := ManagedRoot(s.Config)
	if err := s.validateManagedRoot(ctx, root); err != nil {
		return "", err
	}
	if _, err := command(ctx, root, "rev-parse", "--verify", s.remoteRef()+"^{commit}"); err != nil {
		return "", fmt.Errorf("managed hub branch is unavailable: %w", err)
	}
	return root, nil
}
func (s Store) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := validateHubPath(path); err != nil {
		return nil, err
	}
	lock, err := s.readOnlyLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		return nil, err
	}
	out, err := command(ctx, root, "show", s.remoteRef()+":"+filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > s.Config.MaxReadBytes {
		return nil, fmt.Errorf("hub file exceeds read limit")
	}
	return out, nil
}
func (s Store) ReadJSON(ctx context.Context, path string, out any) error {
	data, err := s.ReadFile(ctx, path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("hub JSON has trailing content")
	}
	return nil
}
func (s Store) List(ctx context.Context, prefix, suffix string) ([]string, error) {
	if err := validateHubPath(prefix); err != nil {
		return nil, err
	}
	lock, err := s.readOnlyLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		return nil, err
	}
	out, err := command(ctx, root, "ls-tree", "-r", "--name-only", s.remoteRef(), "--", prefix)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := []string{}
	for _, line := range lines {
		if line != "" && (suffix == "" || strings.HasSuffix(line, suffix)) {
			result = append(result, line)
			if len(result) > s.Config.MaxListItems {
				return nil, fmt.Errorf("hub list exceeds limit")
			}
		}
	}
	sort.Strings(result)
	return result, nil
}
func (s Store) History(ctx context.Context, path string, limit int) ([]map[string]string, error) {
	if err := validateHubPath(path); err != nil {
		return nil, err
	}
	if limit < 1 || limit > s.Config.MaxListItems {
		return nil, fmt.Errorf("invalid history limit")
	}
	lock, err := s.readOnlyLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		return nil, err
	}
	format := "%H%x00%aI%x00%an%x00%s%x00"
	out, err := command(ctx, root, "log", "--max-count", fmt.Sprint(limit), "--format="+format, s.remoteRef(), "--", path)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	items := []map[string]string{}
	for i := 0; i+3 < len(parts) && len(items) < limit; i += 4 {
		sha := strings.TrimSpace(string(parts[i]))
		if sha == "" {
			continue
		}
		items = append(items, map[string]string{"sha": sha, "date": string(parts[i+1]), "author": string(parts[i+2]), "subject": string(parts[i+3])})
	}
	return items, nil
}
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
	return TransactionResult{Before: before, After: after, Remote: RemoteName, Branch: s.Config.Hub.Branch, Paths: append([]string{}, paths...)}, nil
}
func WriteJSON(worktree, path string, value any) error {
	target, err := safeWritePath(worktree, path)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(target, value, 0o600)
}
func WriteText(worktree, path, text string) error {
	target, err := safeWritePath(worktree, path)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(target, []byte(text), 0o600)
}
func safeWritePath(worktree, path string) (string, error) {
	if err := validateHubPath(path); err != nil {
		return "", err
	}
	root, err := filepath.Abs(worktree)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(filepath.ToSlash(filepath.Clean(path))))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("hub path escapes worktree")
	}
	current := root
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("hub path traverses symlink: %s", current)
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", statErr
		}
	}
	return target, nil
}
func validateHubPath(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) || strings.Contains(path, `\`) {
		return fmt.Errorf("invalid hub path")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	first := strings.Split(clean, "/")[0]
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.EqualFold(first, ".git") {
		return fmt.Errorf("hub path escapes root")
	}
	return nil
}
func (s Store) LastChange(ctx context.Context, path string) (string, error) {
	history, err := s.History(ctx, path, 1)
	if err != nil {
		return "", err
	}
	if len(history) != 1 || history[0]["sha"] == "" {
		return "", fmt.Errorf("no history for %s", path)
	}
	return history[0]["sha"], nil
}
func Timestamp() time.Time { return time.Now().UTC() }
