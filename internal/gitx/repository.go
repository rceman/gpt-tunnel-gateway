package gitx

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func (r Runner) Show(ctx context.Context, p config.ProjectConfig, rev string) (string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return "", err
	}
	root, err := r.revisionRoot(ctx, p, rev)
	if err != nil {
		return "", err
	}
	out, err := r.command(ctx, root, root == p.Mirror, "show", "--no-ext-diff", "--no-textconv", "--format=fuller", "--stat", "--summary", rev)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxReadBytes)
}
func (r Runner) Tree(ctx context.Context, p config.ProjectConfig, rev, path string) ([]string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return nil, err
	}
	if err := validatePath(path); err != nil {
		return nil, err
	}
	root, err := r.revisionRoot(ctx, p, rev)
	if err != nil {
		return nil, err
	}
	args := []string{"ls-tree", "-r", "--name-only", rev}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.command(ctx, root, root == p.Mirror, args...)
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

// TreeLocal reads an exact committed tree from the configured local worktree.
// It never falls back to a mirror or performs refresh/network work.
func (r Runner) TreeLocal(ctx context.Context, p config.ProjectConfig, rev, path string) ([]string, error) {
	if err := model.ValidateCommitSHA(rev); err != nil {
		return nil, err
	}
	if err := validatePath(path); err != nil {
		return nil, err
	}
	args := []string{"ls-tree", "-r", "--name-only", rev}
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

func (r Runner) TreePage(ctx context.Context, p config.ProjectConfig, rev, path string, limit int, cursor string) ([]string, pagination.PageInfo, error) {
	if limit < 1 || limit > r.MaxListItems {
		return nil, pagination.PageInfo{}, fmt.Errorf("invalid tree limit")
	}
	if err := model.ValidateRevision(rev); err != nil {
		return nil, pagination.PageInfo{}, err
	}
	if err := validatePath(path); err != nil {
		return nil, pagination.PageInfo{}, err
	}
	root, err := r.revisionRoot(ctx, p, rev)
	if err != nil {
		return nil, pagination.PageInfo{}, err
	}
	args := []string{"ls-tree", "-r", "--name-only", rev}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.command(ctx, root, root == p.Mirror, args...)
	if err != nil {
		return nil, pagination.PageInfo{}, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return nil, pagination.PageInfo{}, err
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	return pagination.Page("git_tree:"+p.Mirror+"|"+rev+"|"+path, lines, limit, cursor, func(item string) string { return item })
}
func (r Runner) ReadFile(ctx context.Context, p config.ProjectConfig, rev, path string) (string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return "", err
	}
	if err := model.ValidateRelativePath(path); err != nil {
		return "", err
	}
	root, err := r.revisionRoot(ctx, p, rev)
	if err != nil {
		return "", err
	}
	out, err := r.command(ctx, root, root == p.Mirror, "show", rev+":"+filepath.ToSlash(path))
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxReadBytes)
}
func (r Runner) Diff(ctx context.Context, p config.ProjectConfig, from, to string, paths []string) (string, error) {
	if err := model.ValidateRevision(from); err != nil {
		return "", err
	}
	if err := model.ValidateRevision(to); err != nil {
		return "", err
	}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return "", err
		}
	}
	root, err := r.revisionsRoot(ctx, p, from, to)
	if err != nil {
		return "", err
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--find-copies", from, to}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := r.command(ctx, root, root == p.Mirror, args...)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxDiffBytes)
}
func (r Runner) ChangedFiles(ctx context.Context, root, from, to string) ([]string, error) {
	if err := model.ValidateRevision(from); err != nil {
		return nil, err
	}
	if err := model.ValidateRevision(to); err != nil {
		return nil, err
	}
	out, err := r.command(ctx, root, false, "diff", "--name-only", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	for _, path := range lines {
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
	}
	sort.Strings(lines)
	return lines, nil
}
func (r Runner) MergeBase(ctx context.Context, p config.ProjectConfig, left, right string) (string, error) {
	if err := model.ValidateRevision(left); err != nil {
		return "", err
	}
	if err := model.ValidateRevision(right); err != nil {
		return "", err
	}
	root, err := r.revisionsRoot(ctx, p, left, right)
	if err != nil {
		return "", err
	}
	out, err := r.command(ctx, root, root == p.Mirror, "merge-base", left, right)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
func (r Runner) Compare(ctx context.Context, p config.ProjectConfig, left, right string) (Compare, error) {
	base, err := r.MergeBase(ctx, p, left, right)
	if err != nil {
		return Compare{}, err
	}
	root, err := r.revisionsRoot(ctx, p, left, right)
	if err != nil {
		return Compare{}, err
	}
	out, err := r.command(ctx, root, root == p.Mirror, "rev-list", "--left-right", "--count", left+"..."+right)
	if err != nil {
		return Compare{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return Compare{}, fmt.Errorf("unexpected compare output")
	}
	l, _ := strconv.Atoi(fields[0])
	rr, _ := strconv.Atoi(fields[1])
	return Compare{
		MergeBase: base,
		LeftOnly:  l,
		RightOnly: rr,
	}, nil
}

// ResolveMirrorRef resolves exactly one commit-ish in a managed mirror.
