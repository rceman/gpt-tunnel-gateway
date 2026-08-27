package gitx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// WorktreeInfo is the server-owned identity of an existing local worktree.
// The filesystem path is retained for typed service use and is never exposed
// as caller authority.
type WorktreeInfo struct {
	Path   string
	Head   string
	Branch string
}

// ListWorktrees returns the bounded, Git-owned worktree inventory for a
// configured repository. It performs no network or mirror operation.
func (r Runner) ListWorktrees(ctx context.Context, p config.ProjectConfig) ([]WorktreeInfo, error) {
	out, err := r.command(ctx, p.Root, false, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return nil, err
	}
	worktrees := make([]WorktreeInfo, 0)
	var current *WorktreeInfo
	flush := func() {
		if current != nil && current.Path != "" && current.Head != "" {
			worktrees = append(worktrees, *current)
		}
		current = nil
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &WorktreeInfo{Path: filepath.Clean(strings.TrimPrefix(line, "worktree "))}
		case current != nil && strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
		case current != nil && strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		case strings.TrimSpace(line) == "":
			flush()
		}
	}
	flush()
	return worktrees, nil
}

// ResolveWorktree resolves an exact server-owned branch ref to an existing
// worktree of the configured repository. Callers provide a ref, never a path.
func (r Runner) ResolveWorktree(ctx context.Context, p config.ProjectConfig, ref string) (config.ProjectConfig, error) {
	if !strings.HasPrefix(ref, "refs/heads/") || len(ref) == len("refs/heads/") {
		return config.ProjectConfig{}, fmt.Errorf("worktree_ref must be a full local branch ref")
	}
	if err := model.ValidateBranch(ref); err != nil {
		return config.ProjectConfig{}, fmt.Errorf("invalid worktree_ref: %w", err)
	}
	out, err := r.command(ctx, p.Root, false, "worktree", "list", "--porcelain")
	if err != nil {
		return config.ProjectConfig{}, err
	}
	if int64(len(out)) > r.MaxReadBytes {
		return config.ProjectConfig{}, fmt.Errorf("worktree list exceeds read limit")
	}
	var current config.ProjectConfig
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = p
			current.Root = filepath.Clean(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch ") && current.Root != "":
			if strings.TrimSpace(strings.TrimPrefix(line, "branch ")) == ref {
				return current, nil
			}
		}
	}
	return config.ProjectConfig{}, fmt.Errorf("local worktree_ref %q was not found", ref)
}

// ReadLocalFile reads a committed object from an existing local worktree.
// Unlike ReadFile, it never resolves through a mirror or performs network I/O.
func (r Runner) ReadLocalFile(ctx context.Context, p config.ProjectConfig, revision, path string) (string, error) {
	if err := model.ValidateCommitSHA(revision); err != nil {
		return "", err
	}
	if err := model.ValidateRelativePath(path); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Root, false, "show", revision+":"+filepath.ToSlash(path))
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxReadBytes)
}

// ReadWorkingFile reads the current regular file from an existing worktree.
// Git validates the path and excludes repository metadata through its normal
// worktree command boundary.
func (r Runner) ReadWorkingFile(ctx context.Context, p config.ProjectConfig, path string) (string, error) {
	if err := model.ValidateRelativePath(path); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	root, err := filepath.Abs(p.Root)
	if err != nil {
		return "", err
	}
	current, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, current)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("working file escapes repository root")
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if strings.EqualFold(component, ".git") {
			return "", fmt.Errorf("working file path enters repository metadata")
		}
	}
	parent := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		parent = filepath.Join(parent, component)
		info, statErr := os.Lstat(parent)
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("working file path contains a symlink")
		}
		if component != filepath.Base(relative) && !info.IsDir() {
			return "", fmt.Errorf("working file path has a non-directory ancestor")
		}
	}
	info, err := os.Stat(current)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("working path is not a regular file")
	}
	file, err := os.Open(current)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, r.MaxReadBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > r.MaxReadBytes {
		return "", fmt.Errorf("working file exceeds %d bytes", r.MaxReadBytes)
	}
	return string(data), nil
}

// WorkingTreeFiles returns the current tracked and non-ignored untracked
// regular-file paths in a worktree.
func (r Runner) WorkingTreeFiles(ctx context.Context, p config.ProjectConfig, path string) ([]string, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	args := []string{"ls-files", "--cached", "--others", "--exclude-standard", "--full-name"}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.command(ctx, p.Root, false, args...)
	if err != nil {
		return nil, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	return lines, nil
}

// WalkWorkingTreeFiles streams tracked and non-ignored untracked regular-file
// candidates without retaining the complete inventory in memory.
func (r Runner) WalkWorkingTreeFiles(ctx context.Context, p config.ProjectConfig, path string, visit func(string) error) error {
	if err := validatePath(path); err != nil {
		return err
	}
	args := []string{"ls-files", "--cached", "--others", "--exclude-standard", "--full-name", "-z"}
	if path != "" {
		args = append(args, "--", path)
	}
	walk, err := r.commandRecords(ctx, p.Root, false, 0, args...)
	if err != nil {
		return err
	}
	return walk(func(pathName string) error {
		if err := model.ValidateRelativePath(pathName); err != nil {
			return err
		}
		return visit(pathName)
	})
}

func pathSetContains(paths map[string]struct{}, path string) bool {
	if _, ok := paths[path]; ok {
		return true
	}
	for selected := range paths {
		if strings.HasPrefix(path, selected+"/") {
			return true
		}
	}
	return false
}

type diffLinePageCollector struct {
	offset    int64
	limit     int64
	total     int64
	pageLines int64
	maxBytes  int64
	hasMore   bool
	data      bytes.Buffer
	pending   []byte
}

func (c *diffLinePageCollector) done() bool { return c.hasMore }

func (c *diffLinePageCollector) consume(line []byte) error {
	if c.total < c.offset {
		c.total++
		return nil
	}
	if c.pageLines >= c.limit {
		c.total++
		c.hasMore = true
		return ErrStreamLimit
	}
	if c.maxBytes > 0 && int64(c.data.Len()+len(line)) > c.maxBytes {
		return fmt.Errorf("diff page exceeds internal byte limit")
	}
	_, _ = c.data.Write(line)
	c.pageLines++
	c.total++
	return nil
}

func (c *diffLinePageCollector) write(chunk []byte) error {
	c.pending = append(c.pending, chunk...)
	if c.maxBytes > 0 && int64(len(c.pending)) > c.maxBytes {
		return fmt.Errorf("diff line exceeds internal byte limit")
	}
	for {
		index := bytes.IndexByte(c.pending, '\n')
		if index < 0 {
			return nil
		}
		line := c.pending[:index+1]
		c.pending = c.pending[index+1:]
		if err := c.consume(line); err != nil {
			return err
		}
	}
}

func (c *diffLinePageCollector) finish() error {
	if len(c.pending) == 0 {
		return nil
	}
	line := c.pending
	c.pending = nil
	return c.consume(line)
}

func (c *diffLinePageCollector) result() (string, bool, error) {
	if c.offset > c.total {
		return "", false, fmt.Errorf("diff continuation cursor exceeds diff output")
	}
	return c.data.String(), c.hasMore, nil
}

// DiffLocalCommitsPage streams a committed diff and retains only one response
// page plus lookahead. Re-running from the immutable commits makes offsets
// deterministic without materializing the complete diff.
func (r Runner) DiffLocalCommitsPage(ctx context.Context, p config.ProjectConfig, from, to string, paths []string, offset int64, limit int) (string, bool, error) {
	if err := model.ValidateCommitSHA(from); err != nil {
		return "", false, err
	}
	if err := model.ValidateCommitSHA(to); err != nil {
		return "", false, err
	}
	if offset < 0 || limit < 1 {
		return "", false, fmt.Errorf("invalid diff page")
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", from, to, "--"}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return "", false, err
		}
		args = append(args, path)
	}
	collector := diffLinePageCollector{offset: offset, limit: int64(limit), maxBytes: r.MaxDiffBytes}
	exitCode, err := r.streamCommand(ctx, p.Root, false, args, collector.write)
	if err != nil {
		return "", false, err
	}
	if exitCode != 0 {
		return "", false, fmt.Errorf("git diff exited with status %d", exitCode)
	}
	if !collector.hasMore {
		if err := collector.finish(); err != nil {
			return "", false, err
		}
	}
	return collector.result()
}

// DiffWorkingFromBasePage streams tracked changes and regular non-ignored
// untracked files into one bounded page. The repository lock and worktree
// semantics remain Git's; only response buffering is paginated.
func (r Runner) DiffWorkingFromBasePage(ctx context.Context, p config.ProjectConfig, base string, paths []string, offset int64, limit int) (string, bool, error) {
	if err := model.ValidateCommitSHA(base); err != nil {
		return "", false, err
	}
	if offset < 0 || limit < 1 {
		return "", false, fmt.Errorf("invalid diff page")
	}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return "", false, err
		}
	}
	collector := diffLinePageCollector{offset: offset, limit: int64(limit), maxBytes: r.MaxDiffBytes}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--find-copies", base}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	exitCode, err := r.streamCommand(ctx, p.Root, false, args, collector.write)
	if err != nil {
		return "", false, err
	}
	if exitCode != 0 {
		return "", false, fmt.Errorf("git diff exited with status %d", exitCode)
	}
	if !collector.hasMore {
		if err := collector.finish(); err != nil {
			return "", false, err
		}
	}
	if collector.done() {
		return collector.result()
	}
	selected := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		selected[path] = struct{}{}
	}
	untrackedWalk, err := r.commandRecords(ctx, p.Root, false, 0, "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	if err != nil {
		return "", false, err
	}
	if err := untrackedWalk(func(path string) error {
		if collector.done() {
			return ErrStreamLimit
		}
		if path == "" || (len(selected) > 0 && !pathSetContains(selected, path)) {
			return nil
		}
		if err := model.ValidateRelativePath(path); err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Join(p.Root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		code, err := r.streamCommand(ctx, p.Root, false, []string{"diff", "--no-ext-diff", "--no-textconv", "--no-index", "--", "/dev/null", filepath.ToSlash(path)}, collector.write)
		if err != nil {
			return err
		}
		if code != 0 && code != 1 {
			return fmt.Errorf("git diff --no-index exited with status %d", code)
		}
		if !collector.hasMore {
			if err := collector.finish(); err != nil {
				return err
			}
		}
		if collector.done() {
			return ErrStreamLimit
		}
		return nil
	}); err != nil && !errors.Is(err, ErrStreamLimit) {
		return "", false, err
	}
	return collector.result()
}

func (r Runner) WorktreeStatus(ctx context.Context, p config.ProjectConfig) (WorktreeStatus, error) {
	out, err := r.command(ctx, p.Root, false, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return WorktreeStatus{}, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return WorktreeStatus{}, err
	}
	s := WorktreeStatus{
		Porcelain: text,
		Clean:     true,
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			s.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.oid "):
			s.Head = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			fmt.Sscanf(line, "# branch.ab +%d -%d", &s.Ahead, &s.Behind)
		case line != "" && !strings.HasPrefix(line, "# "):
			s.Clean = false
		}
	}
	return s, nil
}

// ChangedWorkingFiles returns the bounded tracked/untracked paths in the
// working tree. It is a typed status operation used by verification scope
// resolution; callers never construct Git commands.
func (r Runner) ChangedWorkingFiles(ctx context.Context, root string) ([]string, error) {
	out, err := r.command(ctx, root, false, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		path := line
		if len(path) > 3 {
			path = strings.TrimSpace(path[3:])
		}
		if arrow := strings.LastIndex(path, " -> "); arrow >= 0 {
			path = strings.TrimSpace(path[arrow+4:])
		}
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// WorktreeFingerprint hashes the exact current worktree state, including
// tracked edits, staged edits, untracked files, deletions, and file modes.
// It is deliberately content-based so a completed verification cannot be
// reused after source bytes change.
func (r Runner) WorktreeFingerprint(ctx context.Context, root string) (string, error) {
	status, err := r.command(ctx, root, false, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return "", err
	}
	if _, err := bounded(status, r.MaxReadBytes); err != nil {
		return "", err
	}
	fileHashes, err := r.WorktreeFileHashes(ctx, root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write(status)
	paths := make([]string, 0, len(fileHashes))
	for path := range fileHashes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		_, _ = io.WriteString(h, path+"\x00"+fileHashes[path]+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WorktreeFileHashes returns content-and-mode identities for every tracked or
// non-ignored untracked path. Missing tracked paths are retained as "missing"
// entries so deletions participate in delta calculation.
func (r Runner) WorktreeFileHashes(ctx context.Context, root string) (map[string]string, error) {
	pathsRaw, err := r.command(ctx, root, false, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	if _, err := bounded(pathsRaw, r.MaxReadBytes); err != nil {
		return nil, err
	}
	paths := strings.Split(strings.TrimSuffix(string(pathsRaw), "\x00"), "\x00")
	hashes := make(map[string]string, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
		value, err := hashWorktreeFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", path, err)
		}
		hashes[path] = value
	}
	return hashes, nil
}

func hashWorktreeFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = io.WriteString(h, info.Mode().String()+"\x00")
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, target)
	} else {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (r Runner) WorktreeDiff(ctx context.Context, p config.ProjectConfig, staged bool) (string, error) {
	args := []string{"diff", "--no-ext-diff", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	out, err := r.command(ctx, p.Root, false, args...)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxDiffBytes)
}

func (r Runner) Resolve(ctx context.Context, root, rev string) (string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return "", err
	}
	out, err := r.command(ctx, root, false, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
func (r Runner) PrepareBranch(ctx context.Context, p config.ProjectConfig, branch, base string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if _, err := r.command(ctx, p.Root, false, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid Git branch: %w", err)
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return err
	}
	if !status.Clean {
		return fmt.Errorf("project worktree is dirty")
	}
	resolved, err := r.Resolve(ctx, p.Root, base)
	if err != nil {
		return err
	}
	if resolved != base {
		return fmt.Errorf("base revision did not resolve exactly")
	}
	_, err = r.command(ctx, p.Root, false, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		if _, e := r.command(ctx, p.Root, false, "merge-base", "--is-ancestor", base, branch); e != nil {
			return fmt.Errorf("existing branch is not based on task base")
		}
		_, err = r.command(ctx, p.Root, false, "switch", branch)
		return err
	}
	_, err = r.command(ctx, p.Root, false, "switch", "-c", branch, base)
	return err
}
func ownedTrainWorktreePath(stateDir, projectID, trainID string) (string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", err
	}
	if _, _, err := model.ParseTrainV2ID(trainID); err != nil {
		return "", err
	}
	if stateDir == "" || strings.ContainsAny(stateDir, "\x00\r\n") {
		return "", fmt.Errorf("invalid train runtime state directory")
	}
	return filepath.Join(stateDir, "train-worktrees", projectID, trainID), nil
}
func ownedCompactTrainWorktreePath(stateDir, projectCode, trainID string) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	code, _, err := model.ParseTrainV2ID(trainID)
	if err != nil || code != projectCode {
		return "", fmt.Errorf("train ID project code does not match project code")
	}
	if stateDir == "" || strings.ContainsAny(stateDir, "\x00\r\n") {
		return "", fmt.Errorf("invalid train runtime state directory")
	}
	return filepath.Join(stateDir, "work", projectCode, trainID[len(projectCode)+1:]), nil
}
