package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const ProtocolRoot = "gpt-tunnel/v1"

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

func cleanEnv(extra ...string) []string {
	keys := []string{"HOME", "PATH", "SSH_AUTH_SOCK", "USER", "LOGNAME", "TMPDIR"}
	out := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C"}
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
func (s Store) remoteRef() string {
	return "refs/remotes/" + s.Config.Hub.Remote + "/" + s.Config.Hub.Branch
}
func (s Store) Refresh(ctx context.Context) error {
	_, err := command(ctx, s.Config.Hub.Root, "fetch", "--prune", "--tags", s.Config.Hub.Remote)
	return err
}
func (s Store) RemoteRevision(ctx context.Context) (string, error) {
	if err := s.Refresh(ctx); err != nil {
		return "", err
	}
	out, err := command(ctx, s.Config.Hub.Root, "rev-parse", "--verify", s.remoteRef()+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
func (s Store) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := validateHubPath(path); err != nil {
		return nil, err
	}
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	out, err := command(ctx, s.Config.Hub.Root, "show", s.remoteRef()+":"+filepath.ToSlash(path))
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
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	out, err := command(ctx, s.Config.Hub.Root, "ls-tree", "-r", "--name-only", s.remoteRef(), "--", prefix)
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
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	format := "%H%x00%aI%x00%an%x00%s%x00"
	out, err := command(ctx, s.Config.Hub.Root, "log", "--max-count", fmt.Sprint(limit), "--format="+format, s.remoteRef(), "--", path)
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
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "hub")
	if err != nil {
		return TransactionResult{}, err
	}
	defer lock.Release()
	statusOut, err := command(ctx, s.Config.Hub.Root, "status", "--porcelain")
	if err != nil {
		return TransactionResult{}, err
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		return TransactionResult{}, fmt.Errorf("hub worktree is dirty")
	}
	before, err := s.RemoteRevision(ctx)
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
	if _, err = command(ctx, s.Config.Hub.Root, "worktree", "add", "--detach", worktree, before); err != nil {
		return TransactionResult{}, err
	}
	defer command(context.Background(), s.Config.Hub.Root, "worktree", "remove", "--force", worktree)
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
	if _, err = command(ctx, worktree, "push", s.Config.Hub.Remote, "HEAD:refs/heads/"+s.Config.Hub.Branch); err != nil {
		return TransactionResult{}, err
	}
	remoteOut, err := command(ctx, s.Config.Hub.Root, "ls-remote", s.Config.Hub.Remote, "refs/heads/"+s.Config.Hub.Branch)
	if err != nil {
		return TransactionResult{}, err
	}
	fields := strings.Fields(string(remoteOut))
	if len(fields) < 1 || fields[0] != after {
		return TransactionResult{}, fmt.Errorf("remote verification failed: got %q want %q", strings.TrimSpace(string(remoteOut)), after)
	}
	return TransactionResult{Before: before, After: after, Remote: s.Config.Hub.Remote, Branch: s.Config.Hub.Branch, Paths: append([]string{}, paths...)}, nil
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
