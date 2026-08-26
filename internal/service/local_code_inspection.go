package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	LocalCodeDefaultMaxBytes = 64 << 10
	LocalCodeMaxBytes        = 256 << 10
	LocalCodeMaxPaths        = 64
	LocalCodeMaxMatches      = 100
	LocalCodeMaxQueryBytes   = 256
	LocalCodeMaxSearchBytes  = 1 << 20
)

type CodeReadInput struct {
	ProjectID   string
	WorktreeRef string
	BaseSHA     string
	Path        string
	Offset      int64
	MaxBytes    int
}

type CodeSearchInput struct {
	ProjectID   string
	WorktreeRef string
	BaseSHA     string
	Query       string
	Paths       []string
	Limit       int
}

type CodeDiffInput struct {
	ProjectID   string
	WorktreeRef string
	BaseSHA     string
	Paths       []string
	MaxBytes    int
}

type CodeIdentity struct {
	ProjectID   string `json:"project_id"`
	WorktreeRef string `json:"worktree_ref"`
	BaseSHA     string `json:"base_sha"`
	CurrentHead string `json:"current_head"`
	Dirty       bool   `json:"dirty"`
}

type CodeReadResult struct {
	CodeIdentity
	Path       string `json:"path"`
	Offset     int64  `json:"offset"`
	TotalBytes int64  `json:"total_bytes"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated"`
}

type CodeSearchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

type CodeSearchResult struct {
	CodeIdentity
	PathsScanned int               `json:"paths_scanned"`
	Matches      []CodeSearchMatch `json:"matches"`
	Truncated    bool              `json:"truncated"`
}

type CodeDiffResult struct {
	CodeIdentity
	Paths     []string `json:"paths"`
	Diff      string   `json:"diff"`
	Truncated bool     `json:"truncated"`
}

type localCodeTarget struct {
	CodeIdentity
	Worktree config.ProjectConfig
}

func (s *Service) resolveLocalCodeTarget(ctx context.Context, projectID, worktreeRef, baseSHA string) (localCodeTarget, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return localCodeTarget{}, err
	}
	if err := model.ValidateCommitSHA(baseSHA); err != nil {
		return localCodeTarget{}, fmt.Errorf("base_sha must be an exact commit SHA: %w", err)
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	if strings.TrimSpace(project.Root) == "" {
		return localCodeTarget{}, fmt.Errorf("project %q has no configured local worktree", projectID)
	}
	if strings.TrimSpace(worktreeRef) == "" {
		return localCodeTarget{}, fmt.Errorf("worktree_ref is required")
	}
	project, err = s.Git.ResolveWorktree(ctx, project, worktreeRef)
	if err != nil {
		return localCodeTarget{}, err
	}
	resolved, err := s.Git.Resolve(ctx, project.Root, baseSHA)
	if err != nil {
		return localCodeTarget{}, fmt.Errorf("resolve local base_sha: %w", err)
	}
	if resolved != baseSHA {
		return localCodeTarget{}, fmt.Errorf("local base_sha resolved to %s, want %s", resolved, baseSHA)
	}
	head, _, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return localCodeTarget{}, err
	}
	return localCodeTarget{
		CodeIdentity: CodeIdentity{ProjectID: projectID, WorktreeRef: worktreeRef, BaseSHA: baseSHA, CurrentHead: head, Dirty: !clean},
		Worktree:     project,
	}, nil
}

func (s *Service) CodeRead(ctx context.Context, in CodeReadInput) (CodeReadResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.WorktreeRef, in.BaseSHA)
	if err != nil {
		return CodeReadResult{}, err
	}
	if in.Offset < 0 {
		return CodeReadResult{}, fmt.Errorf("offset must not be negative")
	}
	if err := model.ValidateRelativePath(in.Path); err != nil {
		return CodeReadResult{}, err
	}
	if err := s.requireVisibleCodePaths(ctx, target, []string{in.Path}); err != nil {
		return CodeReadResult{}, err
	}
	maxBytes, err := localCodeMaxBytes(in.MaxBytes)
	if err != nil {
		return CodeReadResult{}, err
	}
	content, total, truncated, err := readLocalCodeRange(target.Worktree.Root, in.Path, in.Offset, maxBytes)
	if err != nil {
		return CodeReadResult{}, err
	}
	return CodeReadResult{CodeIdentity: target.CodeIdentity, Path: in.Path, Offset: in.Offset, TotalBytes: total, Content: content, Truncated: truncated}, nil
}

func (s *Service) CodeSearch(ctx context.Context, in CodeSearchInput) (CodeSearchResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.WorktreeRef, in.BaseSHA)
	if err != nil {
		return CodeSearchResult{}, err
	}
	paths, err := validateLocalCodePaths(in.Paths, true)
	if err != nil {
		return CodeSearchResult{}, err
	}
	if err := s.requireVisibleCodePaths(ctx, target, paths); err != nil {
		return CodeSearchResult{}, err
	}
	if in.Query == "" || len(in.Query) > LocalCodeMaxQueryBytes || strings.ContainsAny(in.Query, "\x00\r\n") {
		return CodeSearchResult{}, fmt.Errorf("invalid search query")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > LocalCodeMaxMatches {
		return CodeSearchResult{}, fmt.Errorf("invalid search limit")
	}
	result := CodeSearchResult{CodeIdentity: target.CodeIdentity, Matches: []CodeSearchMatch{}}
	var scannedBytes int64
	for pathIndex, path := range paths {
		result.PathsScanned = pathIndex + 1
		data, err := readLocalCodeFile(target.Worktree.Root, path)
		if err != nil {
			return CodeSearchResult{}, err
		}
		scannedBytes += int64(len(data))
		if scannedBytes > LocalCodeMaxSearchBytes {
			return CodeSearchResult{}, fmt.Errorf("code search exceeds scanned-byte bound; narrow paths")
		}
		if strings.IndexByte(string(data), 0) >= 0 {
			continue
		}
		for lineNumber, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, in.Query) {
				continue
			}
			snippet := line
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			result.Matches = append(result.Matches, CodeSearchMatch{Path: path, Line: lineNumber + 1, Snippet: snippet})
			if len(result.Matches) == limit {
				result.Truncated = true
				return result, nil
			}
		}
	}
	result.PathsScanned = len(paths)
	return result, nil
}

func (s *Service) CodeDiff(ctx context.Context, in CodeDiffInput) (CodeDiffResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.WorktreeRef, in.BaseSHA)
	if err != nil {
		return CodeDiffResult{}, err
	}
	if target.Dirty {
		return CodeDiffResult{}, fmt.Errorf("code diff requires a clean local worktree")
	}
	paths, err := validateLocalCodePaths(in.Paths, false)
	if err != nil {
		return CodeDiffResult{}, err
	}
	if len(paths) > 0 {
		if err := s.requireVisibleCodePaths(ctx, target, paths); err != nil {
			return CodeDiffResult{}, err
		}
	}
	maxBytes, err := localCodeMaxBytes(in.MaxBytes)
	if err != nil {
		return CodeDiffResult{}, err
	}
	diff, err := s.Git.WorktreeDiffFrom(ctx, target.Worktree, target.BaseSHA, paths)
	if err != nil {
		return CodeDiffResult{}, err
	}
	result := CodeDiffResult{CodeIdentity: target.CodeIdentity, Paths: paths, Diff: diff}
	if len(diff) > maxBytes {
		result.Diff = diff[:maxBytes]
		result.Truncated = true
	}
	return result, nil
}

func (s *Service) requireVisibleCodePaths(ctx context.Context, target localCodeTarget, paths []string) error {
	visible, err := s.Git.VisibleWorktreePaths(ctx, target.Worktree.Root, paths)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, ok := visible[path]; !ok {
			return fmt.Errorf("code path is not Git-visible: %s", path)
		}
	}
	return nil
}

func validateLocalCodePaths(paths []string, required bool) ([]string, error) {
	if required && len(paths) == 0 {
		return nil, fmt.Errorf("at least one relative code path is required")
	}
	if len(paths) > LocalCodeMaxPaths {
		return nil, fmt.Errorf("too many code paths")
	}
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("duplicate code path %q", path)
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func readLocalCodeRange(root, path string, offset int64, maxBytes int) (string, int64, bool, error) {
	full, err := safeLocalCodePath(root, path)
	if err != nil {
		return "", 0, false, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", 0, false, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, false, fmt.Errorf("code path is not a regular file")
	}
	if offset > info.Size() {
		return "", info.Size(), false, fmt.Errorf("offset exceeds file size")
	}
	file, err := os.Open(full)
	if err != nil {
		return "", 0, false, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", 0, false, err
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return "", 0, false, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return string(data), info.Size(), truncated, nil
}

func readLocalCodeFile(root, path string) ([]byte, error) {
	full, err := safeLocalCodePath(root, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > LocalCodeMaxBytes {
		return nil, fmt.Errorf("code search file exceeds bounded regular-file limit")
	}
	return os.ReadFile(full)
}

func safeLocalCodePath(root, path string) (string, error) {
	if err := model.ValidateRelativePath(path); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		return "", err
	}
	resolvedFull := filepath.Join(resolvedParent, filepath.Base(full))
	rel, err := filepath.Rel(resolvedRoot, resolvedFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("code path escapes configured local worktree")
	}
	if strings.EqualFold(strings.Split(filepath.ToSlash(rel), "/")[0], ".git") {
		return "", fmt.Errorf("code path is not reviewable")
	}
	if info, statErr := os.Lstat(full); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("code path symlink is not reviewable")
	}
	return full, nil
}

func localCodeMaxBytes(requested int) (int, error) {
	if requested == 0 {
		return LocalCodeDefaultMaxBytes, nil
	}
	if requested < 1 || requested > LocalCodeMaxBytes {
		return 0, fmt.Errorf("invalid code byte bound")
	}
	return requested, nil
}
