package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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

// VisibleWorktreePaths returns the exact tracked or non-ignored paths among
// the requested paths. Git remains the authority for visibility; filesystem
// existence is checked separately by code inspection.
func (r Runner) VisibleWorktreePaths(ctx context.Context, root string, paths []string) (map[string]struct{}, error) {
	if len(paths) == 0 {
		return map[string]struct{}{}, nil
	}
	args := []string{"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--"}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
		args = append(args, path)
	}
	out, err := r.command(ctx, root, false, args...)
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > r.MaxReadBytes {
		return nil, fmt.Errorf("visible code path list exceeds read limit")
	}
	wanted := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		wanted[path] = struct{}{}
	}
	visible := make(map[string]struct{}, len(paths))
	for _, path := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if _, ok := wanted[path]; ok {
			visible[path] = struct{}{}
		}
	}
	return visible, nil
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

// WorktreeDiffFrom compares the configured local worktree with an exact local
// base. It never resolves through a mirror or performs network I/O.
func (r Runner) WorktreeDiffFrom(ctx context.Context, p config.ProjectConfig, from string, paths []string) (string, error) {
	if err := model.ValidateCommitSHA(from); err != nil {
		return "", err
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", from, "--"}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return "", err
		}
		args = append(args, path)
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
