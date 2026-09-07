package gitx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type WorktreeInfo struct {
	Path   string
	Head   string
	Branch string
}

// WorktreeInventory is one bounded snapshot of Git's worktree registry. The
// registry is only used to resolve server-owned branch identities; it is not
// an inventory authority.

type WorktreeInventory struct {
	byBranch map[string]config.ProjectConfig
}

// LoadWorktreeInventory enumerates Git worktrees exactly once.

func (r Runner) LoadWorktreeInventory(ctx context.Context, p config.ProjectConfig) (WorktreeInventory, error) {
	worktrees, err := r.ListWorktrees(ctx, p)
	if err != nil {
		return WorktreeInventory{}, err
	}
	byBranch := make(map[string]config.ProjectConfig, len(worktrees))
	for _, worktree := range worktrees {
		if worktree.Branch == "" {
			continue
		}
		if _, exists := byBranch[worktree.Branch]; exists {
			return WorktreeInventory{}, fmt.Errorf("ambiguous Git worktree branch %q", worktree.Branch)
		}
		resolved := p
		resolved.Root = filepath.Clean(worktree.Path)
		byBranch[worktree.Branch] = resolved
	}
	return WorktreeInventory{byBranch: byBranch}, nil
}

// Resolve validates and resolves a server-owned full local branch ref from
// the already-loaded inventory without invoking Git again.

func (i WorktreeInventory) Resolve(ref string) (config.ProjectConfig, error) {
	if !strings.HasPrefix(ref, "refs/heads/") || len(ref) == len("refs/heads/") {
		return config.ProjectConfig{}, fmt.Errorf("worktree_ref must be a full local branch ref")
	}
	if err := model.ValidateBranch(ref); err != nil {
		return config.ProjectConfig{}, fmt.Errorf("invalid worktree_ref: %w", err)
	}
	worktree, ok := i.byBranch[ref]
	if !ok {
		return config.ProjectConfig{}, fmt.Errorf("local worktree_ref %q was not found", ref)
	}
	return worktree, nil
}

// ResolveHotfixWorktreeFromInventory applies the server-owned hotfix path
// check to an already-loaded Git inventory.

func (r Runner) ResolveHotfixWorktreeFromInventory(inventory WorktreeInventory, stateDir, projectID, ref string) (config.ProjectConfig, error) {
	slug, err := hotfixSlugFromRef(ref)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	expected, _, err := hotfixWorktreePath(stateDir, projectID, slug)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	worktree, err := inventory.Resolve(ref)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	actual, err := filepath.Abs(worktree.Root)
	if err != nil || filepath.Clean(actual) != filepath.Clean(expected) {
		return config.ProjectConfig{}, fmt.Errorf("hotfix worktree is not server-owned")
	}
	worktree.Root = actual
	return worktree, nil
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

// DiffLineVisitor receives each semantic diff line and its stable zero-based
// offset in the complete diff stream. Returning ErrStreamLimit stops Git
// immediately after the current line without turning it into a page driver.

type DiffLineVisitor func(offset int64, line []byte) error

type diffLineStream struct {
	offset          int64
	total           int64
	visitor         DiffLineVisitor
	maxPendingBytes int64
	pending         []byte
	stopped         bool
}

func (s *diffLineStream) consume(line []byte) error {
	lineOffset := s.total
	s.total++
	if lineOffset < s.offset {
		return nil
	}
	if err := s.visitor(lineOffset, line); err != nil {
		if errors.Is(err, ErrStreamLimit) {
			s.stopped = true
			return ErrStreamLimit
		}
		return err
	}
	return nil
}
