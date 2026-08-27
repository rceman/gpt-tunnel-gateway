package service

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

const (
	LocalCodeDefaultMaxBytes = 64 << 10
	LocalCodeMaxBytes        = 256 << 10
	LocalCodeMaxPaths        = 64
	LocalCodeMaxMatches      = 100
	LocalCodeMaxQueryBytes   = 256
	LocalCodeMaxSearchBytes  = 1 << 20
	LocalCodeMaxLines        = 1000
	LocalCodeMaxSearchItems  = 4096
	LocalCodeMaxPatterns     = 32
)

type CodeWorktreeInput struct {
	ProjectID string
	Query     string
	Cursor    string
	Limit     int
}

type CodeWorktreeItem struct {
	Selector string `json:"selector"`
	Kind     string `json:"kind"`
	Dirty    bool   `json:"dirty"`
	Label    string `json:"label,omitempty"`
	TrainID  string `json:"train_id,omitempty"`
}

type CodeWorktreeResult struct {
	Items      []CodeWorktreeItem `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
}

type CodeTreeInput struct {
	ProjectID string
	Worktree  string
	Path      string
	Query     string
	Cursor    string
	Limit     int
	Live      bool
}

type CodeTreeResult struct {
	CodeIdentity
	Paths      []string `json:"paths"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

type CodeReadInput struct {
	ProjectID string
	Worktree  string
	Path      string
	StartLine int
	LineCount int
	Cursor    string
	Live      bool

	// Internal-only fields retained for old direct service fixtures. Public MCP
	// decoders do not expose caller authority through these fields.
	WorktreeRef string
	BaseSHA     string
	Offset      int64
	MaxBytes    int
}

type CodeSearchInput struct {
	ProjectID string
	Worktree  string
	Query     string
	Paths     []string
	Include   []string
	Exclude   []string
	Limit     int
	Cursor    string
	Live      bool

	WorktreeRef string
	BaseSHA     string
}

type CodeDiffInput struct {
	ProjectID string
	Worktree  string
	Paths     []string
	MaxBytes  int
	Cursor    string
	Live      bool

	WorktreeRef string
	BaseSHA     string
}

type CodeIdentity struct {
	Worktree string `json:"worktree"`
	Dirty    bool   `json:"dirty"`
	Live     bool   `json:"live"`

	ProjectID   string `json:"-"`
	WorktreeRef string `json:"-"`
	BaseSHA     string `json:"-"`
	CurrentHead string `json:"-"`
}

type CodeReadResult struct {
	CodeIdentity
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`

	Offset     int64 `json:"-"`
	TotalBytes int64 `json:"-"`
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
	NextCursor   string            `json:"next_cursor,omitempty"`
	HasMore      bool              `json:"has_more"`
}

type CodeDiffResult struct {
	CodeIdentity
	Paths      []string `json:"paths"`
	Diff       string   `json:"diff"`
	Truncated  bool     `json:"truncated"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

type localCodeTarget struct {
	CodeIdentity
	ProjectWorktree config.ProjectConfig
	Kind            string
	TrainID         string
}

type codeWorktreeCandidate struct {
	localCodeTarget
	Label string
}

func (s *Service) codeTrainRecords(ctx context.Context, projectID string) ([]model.TrainV2, error) {
	if s.Durability != nil {
		return s.sharedTrains(ctx, projectID)
	}
	return nil, fmt.Errorf("Shared Train authority is unavailable for local worktree discovery")
}

func activeCodeTrainStatus(status string) bool {
	switch status {
	case model.TrainV2Planned, model.TrainV2Running, model.TrainV2Paused, model.TrainV2Blocked, model.TrainV2ReadyForIntegration:
		return true
	default:
		return false
	}
}

func codeSelector(trainID, head string) (string, error) {
	if len(head) < 8 || model.ValidateCommitSHA(head) != nil {
		return "", fmt.Errorf("invalid worktree HEAD")
	}
	if trainID == "" {
		return "WT-MAIN-" + strings.ToLower(head[:8]), nil
	}
	_, number, err := model.ParseTrainV2ID(trainID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("WT-TRN%d-%s", number, strings.ToLower(head[:8])), nil
}

func (s *Service) codeWorktreeCandidates(ctx context.Context, projectID string) ([]codeWorktreeCandidate, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return nil, err
	}
	worktrees, err := s.Git.ListWorktrees(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list local worktrees: %w", err)
	}
	trains, err := s.codeTrainRecords(ctx, projectID)
	if err != nil && !IsNotFound(err) {
		return nil, fmt.Errorf("read managed Train worktrees: %w", err)
	}
	managed := make(map[string]model.TrainV2, len(trains))
	for _, train := range trains {
		if train.ProjectID == projectID && activeCodeTrainStatus(train.Status) && train.Historical == nil {
			managed[train.ID] = train
		}
	}
	candidates := make([]codeWorktreeCandidate, 0, len(worktrees))
	seen := make(map[string]struct{})
	for _, info := range worktrees {
		worktree := project
		worktree.Root = info.Path
		status, statusErr := s.Git.WorktreeStatus(ctx, worktree)
		if statusErr != nil {
			return nil, fmt.Errorf("read worktree status: %w", statusErr)
		}
		trainID, kind, label := "", "", ""
		if filepath.Clean(info.Path) == filepath.Clean(project.Root) {
			kind, label = "main", "main"
		} else if strings.HasPrefix(info.Branch, "refs/heads/train/") {
			candidateID := strings.TrimPrefix(info.Branch, "refs/heads/train/")
			if _, ok := managed[candidateID]; !ok {
				continue
			}
			trainID, kind, label = candidateID, "train", candidateID
		} else {
			continue
		}
		selector, selectorErr := codeSelector(trainID, status.Head)
		if selectorErr != nil {
			return nil, selectorErr
		}
		if _, exists := seen[selector]; exists {
			return nil, fmt.Errorf("ambiguous worktree selector %q", selector)
		}
		seen[selector] = struct{}{}
		base := status.Head
		if trainID != "" {
			base = ""
			train := managed[trainID]
			for _, item := range train.Items {
				for _, attempt := range item.Attempts {
					if attempt.Status == model.TrainV2AttemptRunning && model.ValidateCommitSHA(attempt.StartHead) == nil {
						base = attempt.StartHead
					}
				}
			}
		}
		candidates = append(candidates, codeWorktreeCandidate{
			localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{ProjectID: projectID, Worktree: selector, Dirty: !status.Clean, CurrentHead: status.Head, BaseSHA: base, WorktreeRef: info.Branch}, ProjectWorktree: worktree, Kind: kind, TrainID: trainID},
			Label:           label,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CodeIdentity.Worktree < candidates[j].CodeIdentity.Worktree })
	return candidates, nil
}

func (s *Service) resolveLocalCodeTarget(ctx context.Context, projectID, selector string, live bool) (localCodeTarget, error) {
	if selector == "" {
		return localCodeTarget{}, fmt.Errorf("worktree selector is required")
	}
	candidates, err := s.codeWorktreeCandidates(ctx, projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	for _, candidate := range candidates {
		if candidate.CodeIdentity.Worktree != selector {
			continue
		}
		if !live && candidate.Dirty {
			return localCodeTarget{}, fmt.Errorf("worktree selector %q is dirty; set live=true for bounded observation", selector)
		}
		if candidate.TrainID != "" && candidate.BaseSHA == "" {
			return localCodeTarget{}, fmt.Errorf("worktree selector %q has no authoritative Train base", selector)
		}
		candidate.Live = live
		if candidate.TrainID != "" {
			ancestor, ancestorErr := s.Git.IsAncestor(ctx, candidate.ProjectWorktree.Root, candidate.BaseSHA, candidate.CurrentHead)
			if ancestorErr != nil || !ancestor {
				return localCodeTarget{}, fmt.Errorf("worktree selector %q has an invalid authoritative Train base", selector)
			}
		}
		return candidate.localCodeTarget, nil
	}
	if kind, number, _, parseErr := parseCodeSelector(selector); parseErr == nil {
		for _, candidate := range candidates {
			if candidate.Kind != kind || (kind == "train" && number != candidateTrainNumber(candidate.TrainID)) {
				continue
			}
			return localCodeTarget{}, fmt.Errorf("stale worktree selector %q; current selector is %q", selector, candidate.CodeIdentity.Worktree)
		}
	}
	return localCodeTarget{}, fmt.Errorf("worktree selector %q was not found in this project", selector)
}

func candidateTrainNumber(trainID string) uint64 {
	_, number, _ := model.ParseTrainV2ID(trainID)
	return number
}

func parseCodeSelector(selector string) (string, uint64, string, error) {
	if strings.HasPrefix(selector, "WT-MAIN-") && len(selector) == len("WT-MAIN-")+8 {
		prefix := selector[len("WT-MAIN-"):]
		if !validSelectorPrefix(prefix) {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		return "main", 0, prefix, nil
	}
	if !strings.HasPrefix(selector, "WT-TRN") {
		return "", 0, "", fmt.Errorf("invalid worktree selector")
	}
	parts := strings.Split(strings.TrimPrefix(selector, "WT-TRN"), "-")
	if len(parts) != 2 || len(parts[1]) != 8 || (len(parts[0]) > 1 && strings.HasPrefix(parts[0], "0")) || !validSelectorPrefix(parts[1]) {
		return "", 0, "", fmt.Errorf("invalid worktree selector")
	}
	number, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return "", 0, "", err
	}
	return "train", number, parts[1], nil
}

func validSelectorPrefix(prefix string) bool {
	if len(prefix) != 8 {
		return false
	}
	for _, char := range prefix {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func (s *Service) CodeWorktree(ctx context.Context, in CodeWorktreeInput) (CodeWorktreeResult, error) {
	candidates, err := s.codeWorktreeCandidates(ctx, in.ProjectID)
	if err != nil {
		return CodeWorktreeResult{}, err
	}
	query := strings.TrimSpace(in.Query)
	if len(query) > LocalCodeMaxQueryBytes || strings.ContainsAny(query, "\x00\r\n") {
		return CodeWorktreeResult{}, fmt.Errorf("invalid worktree query")
	}
	items := make([]CodeWorktreeItem, 0, len(candidates))
	for _, candidate := range candidates {
		if query != "" && !strings.Contains(candidate.CodeIdentity.Worktree, query) && !strings.Contains(candidate.Label, query) && !strings.Contains(candidate.TrainID, query) {
			continue
		}
		items = append(items, CodeWorktreeItem{Selector: candidate.CodeIdentity.Worktree, Kind: candidate.Kind, Dirty: candidate.Dirty, Label: candidate.Label, TrainID: candidate.TrainID})
	}
	limit, err := PublicCollectionLimit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return CodeWorktreeResult{}, err
	}
	page, info, err := pagination.Page("code-worktree|"+in.ProjectID+"|"+query, items, limit, in.Cursor, func(item CodeWorktreeItem) string { return item.Selector })
	if err != nil {
		return CodeWorktreeResult{}, err
	}
	return CodeWorktreeResult{Items: page, NextCursor: info.NextCursor, HasMore: info.HasMore}, nil
}

func (s *Service) CodeTree(ctx context.Context, in CodeTreeInput) (CodeTreeResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.Worktree, in.Live)
	if err != nil {
		return CodeTreeResult{}, err
	}
	if err := model.ValidateRelativePath(in.Path); err != nil && in.Path != "" {
		return CodeTreeResult{}, err
	}
	if len(in.Query) > LocalCodeMaxQueryBytes || strings.ContainsAny(in.Query, "\x00\r\n") {
		return CodeTreeResult{}, fmt.Errorf("invalid tree query")
	}
	var paths []string
	if target.Live {
		paths, err = s.Git.WorkingTreeFiles(ctx, target.ProjectWorktree, in.Path)
	} else {
		paths, err = s.Git.Tree(ctx, target.ProjectWorktree, target.CurrentHead, in.Path)
	}
	if err != nil {
		return CodeTreeResult{}, err
	}
	filtered := make([]string, 0, len(paths))
	for _, pathName := range paths {
		if in.Query == "" || strings.Contains(pathName, in.Query) {
			filtered = append(filtered, pathName)
		}
	}
	limit, err := PublicCollectionLimit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return CodeTreeResult{}, err
	}
	kind := "code-tree|" + target.CodeIdentity.Worktree + "|" + in.Path + "|" + in.Query + "|" + strconv.FormatBool(target.Live)
	page, info, err := pagination.Page(kind, filtered, limit, in.Cursor, func(item string) string { return item })
	if err != nil {
		return CodeTreeResult{}, err
	}
	return CodeTreeResult{CodeIdentity: target.CodeIdentity, Paths: page, NextCursor: info.NextCursor, HasMore: info.HasMore}, nil
}

func (s *Service) resolveLegacyCodeTarget(ctx context.Context, projectID, ref, base string) (localCodeTarget, error) {
	if model.ValidateCommitSHA(base) != nil {
		return localCodeTarget{}, fmt.Errorf("base_sha must be an exact commit SHA")
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return localCodeTarget{}, err
	}
	project, err = s.Git.ResolveWorktree(ctx, project, ref)
	if err != nil {
		return localCodeTarget{}, err
	}
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return localCodeTarget{}, err
	}
	if !status.Clean {
		return localCodeTarget{}, fmt.Errorf("local worktree_ref %q is dirty", ref)
	}
	resolved, err := s.Git.Resolve(ctx, project.Root, base)
	if err != nil || resolved != base {
		return localCodeTarget{}, fmt.Errorf("base_sha is not an exact local commit")
	}
	ancestor, err := s.Git.IsAncestor(ctx, project.Root, base, status.Head)
	if err != nil || !ancestor {
		return localCodeTarget{}, fmt.Errorf("base_sha %s is not an ancestor of local HEAD %s", base, status.Head)
	}
	return localCodeTarget{CodeIdentity: CodeIdentity{ProjectID: projectID, Worktree: ref, WorktreeRef: ref, BaseSHA: base, CurrentHead: status.Head, Dirty: false}, ProjectWorktree: project, Kind: "main"}, nil
}

func (s *Service) codeTarget(ctx context.Context, projectID, selector, legacyRef, legacyBase string, live bool) (localCodeTarget, error) {
	if selector != "" {
		return s.resolveLocalCodeTarget(ctx, projectID, selector, live)
	}
	if legacyRef != "" || legacyBase != "" {
		if live {
			return localCodeTarget{}, fmt.Errorf("legacy code authority is not valid with live=true")
		}
		return s.resolveLegacyCodeTarget(ctx, projectID, legacyRef, legacyBase)
	}
	return localCodeTarget{}, fmt.Errorf("worktree selector is required")
}

func validateCodePatterns(patterns []string) error {
	if len(patterns) > LocalCodeMaxPatterns {
		return fmt.Errorf("too many path patterns")
	}
	for _, pattern := range patterns {
		if pattern == "" || len(pattern) > 256 || strings.ContainsAny(pattern, "\x00\r\n\\") {
			return fmt.Errorf("invalid path pattern")
		}
		if _, err := path.Match(pattern, "probe"); err != nil {
			return fmt.Errorf("invalid path pattern: %w", err)
		}
	}
	return nil
}

func codePathMatches(pathName string, include, exclude []string) bool {
	if len(include) > 0 {
		matched := false
		for _, pattern := range include {
			if ok, _ := path.Match(pattern, pathName); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pattern := range exclude {
		if ok, _ := path.Match(pattern, pathName); ok {
			return false
		}
	}
	return true
}

func (s *Service) codePaths(ctx context.Context, target localCodeTarget, paths, include, exclude []string) ([]string, error) {
	selected, err := validateLocalCodePaths(paths, false)
	if err != nil {
		return nil, err
	}
	if err := validateCodePatterns(include); err != nil {
		return nil, err
	}
	if err := validateCodePatterns(exclude); err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		if target.Live {
			selected, err = s.Git.WorkingTreeFiles(ctx, target.ProjectWorktree, "")
		} else {
			selected, err = s.Git.Tree(ctx, target.ProjectWorktree, target.CurrentHead, "")
		}
		if err != nil {
			return nil, err
		}
	}
	filtered := selected[:0]
	for _, pathName := range selected {
		if codePathMatches(pathName, include, exclude) {
			filtered = append(filtered, pathName)
		}
	}
	sort.Strings(filtered)
	return filtered, nil
}

func (s *Service) readCodeFile(ctx context.Context, target localCodeTarget, pathName string) (string, error) {
	if target.Live {
		return s.Git.ReadWorkingFile(ctx, target.ProjectWorktree, pathName)
	}
	return s.Git.ReadLocalFile(ctx, target.ProjectWorktree, target.CurrentHead, pathName)
}

func (s *Service) CodeRead(ctx context.Context, in CodeReadInput) (CodeReadResult, error) {
	target, err := s.codeTarget(ctx, in.ProjectID, in.Worktree, in.WorktreeRef, in.BaseSHA, in.Live)
	if err != nil {
		return CodeReadResult{}, err
	}
	if err := model.ValidateRelativePath(in.Path); err != nil {
		return CodeReadResult{}, err
	}
	content, err := s.readCodeFile(ctx, target, in.Path)
	if err != nil {
		return CodeReadResult{}, err
	}
	if in.Worktree == "" {
		if in.Offset < 0 {
			return CodeReadResult{}, fmt.Errorf("offset must not be negative")
		}
		maxBytes, err := localCodeMaxBytes(in.MaxBytes)
		if err != nil {
			return CodeReadResult{}, err
		}
		if in.Offset > int64(len(content)) {
			return CodeReadResult{}, fmt.Errorf("offset exceeds file size")
		}
		data := []byte(content)[in.Offset:]
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
		}
		return CodeReadResult{CodeIdentity: target.CodeIdentity, Path: in.Path, Offset: in.Offset, TotalBytes: int64(len(content)), Content: string(data), Truncated: truncated, HasMore: truncated}, nil
	}
	lines := strings.Split(content, "\n")
	start := in.StartLine
	if start == 0 {
		start = 1
	}
	if in.Cursor != "" {
		keys := make([]string, len(lines)+1)
		for index := range keys {
			keys[index] = strconv.Itoa(index + 1)
		}
		resolved, resolveErr := pagination.Resolve(in.Cursor, "code-read|"+target.CodeIdentity.Worktree+"|"+in.Path+"|"+strconv.FormatBool(target.Live), keys)
		if resolveErr != nil {
			return CodeReadResult{}, resolveErr
		}
		start, err = strconv.Atoi(resolved)
		if err != nil {
			return CodeReadResult{}, fmt.Errorf("invalid code read cursor")
		}
	}
	count := in.LineCount
	if count == 0 {
		count = 200
	}
	if start < 1 || count < 1 || count > LocalCodeMaxLines {
		return CodeReadResult{}, fmt.Errorf("invalid code line range")
	}
	if start > len(lines)+1 {
		return CodeReadResult{}, fmt.Errorf("code read start_line exceeds file")
	}
	end := start + count - 1
	if end > len(lines) {
		end = len(lines)
	}
	result := CodeReadResult{CodeIdentity: target.CodeIdentity, Path: in.Path, StartLine: start, EndLine: end, TotalLines: len(lines)}
	if start <= len(lines) {
		result.Content = strings.Join(lines[start-1:end], "\n")
	}
	if end < len(lines) {
		result.Truncated, result.HasMore = true, true
		result.NextCursor = pagination.Encode("code-read|"+target.CodeIdentity.Worktree+"|"+in.Path+"|"+strconv.FormatBool(target.Live), strconv.Itoa(end+1))
	}
	return result, nil
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
	for _, pathName := range paths {
		if err := model.ValidateRelativePath(pathName); err != nil {
			return nil, err
		}
		if _, exists := seen[pathName]; exists {
			return nil, fmt.Errorf("duplicate code path %q", pathName)
		}
		seen[pathName] = struct{}{}
		result = append(result, pathName)
	}
	sort.Strings(result)
	return result, nil
}

func (s *Service) CodeSearch(ctx context.Context, in CodeSearchInput) (CodeSearchResult, error) {
	target, err := s.codeTarget(ctx, in.ProjectID, in.Worktree, in.WorktreeRef, in.BaseSHA, in.Live)
	if err != nil {
		return CodeSearchResult{}, err
	}
	if in.Query == "" || len(in.Query) > LocalCodeMaxQueryBytes || strings.ContainsAny(in.Query, "\x00\r\n") {
		return CodeSearchResult{}, fmt.Errorf("invalid search query")
	}
	limit, err := PublicCollectionLimit(in.Limit, LocalCodeMaxMatches)
	if err != nil {
		return CodeSearchResult{}, err
	}
	paths, err := s.codePaths(ctx, target, in.Paths, in.Include, in.Exclude)
	if err != nil {
		return CodeSearchResult{}, err
	}
	result := CodeSearchResult{CodeIdentity: target.CodeIdentity, Matches: []CodeSearchMatch{}, PathsScanned: len(paths)}
	var scannedBytes int64
	for _, pathName := range paths {
		data, readErr := s.readCodeFile(ctx, target, pathName)
		if readErr != nil {
			return CodeSearchResult{}, readErr
		}
		if len(data) > LocalCodeMaxBytes {
			return CodeSearchResult{}, fmt.Errorf("code search file exceeds bounded object limit")
		}
		scannedBytes += int64(len(data))
		if scannedBytes > LocalCodeMaxSearchBytes {
			return CodeSearchResult{}, fmt.Errorf("code search exceeds scanned-byte bound; narrow paths")
		}
		if strings.IndexByte(data, 0) >= 0 {
			continue
		}
		for lineNumber, line := range strings.Split(data, "\n") {
			if !strings.Contains(line, in.Query) {
				continue
			}
			if len(result.Matches) >= LocalCodeMaxSearchItems {
				return CodeSearchResult{}, fmt.Errorf("code search result set exceeds bounded limit; narrow query or paths")
			}
			snippet := line
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			result.Matches = append(result.Matches, CodeSearchMatch{Path: pathName, Line: lineNumber + 1, Snippet: snippet})
		}
	}
	kind := "code-search|" + target.CodeIdentity.Worktree + "|" + in.Query + "|" + strings.Join(paths, "\x00") + "|" + strconv.FormatBool(target.Live)
	page, info, err := pagination.Page(kind, result.Matches, limit, in.Cursor, func(match CodeSearchMatch) string { return match.Path + ":" + strconv.Itoa(match.Line) })
	if err != nil {
		return CodeSearchResult{}, err
	}
	result.Matches, result.NextCursor, result.HasMore = page, info.NextCursor, info.HasMore
	result.Truncated = info.HasMore
	return result, nil
}

func (s *Service) CodeDiff(ctx context.Context, in CodeDiffInput) (CodeDiffResult, error) {
	target, err := s.codeTarget(ctx, in.ProjectID, in.Worktree, in.WorktreeRef, in.BaseSHA, in.Live)
	if err != nil {
		return CodeDiffResult{}, err
	}
	paths, err := validateLocalCodePaths(in.Paths, false)
	if err != nil {
		return CodeDiffResult{}, err
	}
	maxBytes, err := localCodeMaxBytes(in.MaxBytes)
	if err != nil {
		return CodeDiffResult{}, err
	}
	var diff string
	if target.Live {
		diff, err = s.Git.DiffWorkingFromBase(ctx, target.ProjectWorktree, target.BaseSHA, paths)
	} else {
		diff, err = s.Git.DiffLocalCommits(ctx, target.ProjectWorktree, target.BaseSHA, target.CurrentHead, paths)
	}
	if err != nil {
		return CodeDiffResult{}, err
	}
	kind := "code-diff|" + target.CodeIdentity.Worktree + "|" + strings.Join(paths, "\x00") + "|" + strconv.FormatBool(target.Live)
	keys := []string{"0"}
	for offset := maxBytes; offset < len(diff); offset += maxBytes {
		keys = append(keys, strconv.Itoa(offset))
	}
	if len(diff) > 0 {
		keys = append(keys, strconv.Itoa(len(diff)))
	}
	offset := 0
	if in.Cursor != "" {
		resolved, resolveErr := pagination.Resolve(in.Cursor, kind, keys)
		if resolveErr != nil {
			return CodeDiffResult{}, resolveErr
		}
		offset, err = strconv.Atoi(resolved)
		if err != nil || offset < 0 || offset > len(diff) {
			return CodeDiffResult{}, fmt.Errorf("invalid code diff cursor")
		}
	}
	end := offset + maxBytes
	if end > len(diff) {
		end = len(diff)
	}
	result := CodeDiffResult{CodeIdentity: target.CodeIdentity, Paths: paths, Diff: diff[offset:end]}
	if end < len(diff) {
		result.Truncated, result.HasMore = true, true
		result.NextCursor = pagination.Encode(kind, strconv.Itoa(end))
	}
	return result, nil
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
