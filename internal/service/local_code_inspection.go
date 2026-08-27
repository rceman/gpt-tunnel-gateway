package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const (
	LocalCodeDefaultMaxBytes = 64 << 10
	LocalCodeMaxBytes        = 256 << 10
	LocalCodeMaxPaths        = 64
	LocalCodeMaxMatches      = 100
	LocalCodeMaxQueryBytes   = 256
	LocalCodeMaxLines        = 1000
	LocalCodeMaxPatterns     = 32
	LocalCodeMaxScanPaths    = 4096
	LocalCodeScanLookahead   = LocalCodeMaxScanPaths + 1
)

type CodeSelectorErrorKind string

const (
	CodeSelectorNotFound CodeSelectorErrorKind = "not_found"
	CodeSelectorStale    CodeSelectorErrorKind = "stale"
)

type CodeSelectorError struct {
	Kind     CodeSelectorErrorKind
	Selector string
	Current  string
}

var errCodePageDone = errors.New("code page complete")
var errCodeScanLimit = errors.New("code scan budget reached")

func (e *CodeSelectorError) Error() string {
	if e.Kind == CodeSelectorStale && e.Current != "" {
		return fmt.Sprintf("worktree selector %q is stale; current selector is %q", e.Selector, e.Current)
	}
	return fmt.Sprintf("worktree selector %q was not found in this project", e.Selector)
}

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
	Head     string `json:"head"`
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
}

type CodeDiffInput struct {
	ProjectID string
	Worktree  string
	Paths     []string
	MaxBytes  int
	Cursor    string
	Live      bool
}

type CodeIdentity struct {
	Worktree string `json:"worktree"`
	Dirty    bool   `json:"dirty"`
	Live     bool   `json:"live"`

	ProjectID   string `json:"-"`
	CurrentHead string `json:"head"`
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
	DiffBase        string
}

type codeWorktreeCandidate struct {
	localCodeTarget
	Label string
}

func codeTrainWorktreePath(stateDir, projectID string, project config.ProjectConfig, trainID string, runtime *trainv2.RuntimeBinding) (string, error) {
	if err := model.ValidateProjectCode(project.ProjectCode); err != nil {
		return "", fmt.Errorf("project %q has no valid managed project code: %w", projectID, err)
	}
	expected, err := trainv2.CompactWorktreePath(stateDir, project.ProjectCode, trainID)
	if err != nil {
		return "", err
	}
	if runtime != nil {
		if runtime.ProjectID != projectID || runtime.TrainID != trainID || runtime.ProjectCode != project.ProjectCode || filepath.Clean(runtime.WorktreePath) != filepath.Clean(expected) {
			return "", fmt.Errorf("managed Train %s has an invalid runtime worktree binding", trainID)
		}
	}
	return expected, nil
}

func codeCursorKind(operation string, target localCodeTarget, suffix string) string {
	return operation + "|" + target.ProjectID + "|" + target.CodeIdentity.Worktree + "|" + target.CurrentHead + "|" + target.DiffBase + "|" + suffix
}

func (s *Service) codeTrainRecords(ctx context.Context, projectID string) ([]model.TrainV2, error) {
	if s.Durability == nil {
		return nil, fmt.Errorf("Shared durability unavailable for code worktree discovery")
	}
	return s.sharedTrains(ctx, projectID)
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

func (s *Service) validateCodeSelectorIdentity(ctx context.Context, worktree config.ProjectConfig, head string) error {
	if len(head) < 8 || model.ValidateCommitSHA(head) != nil {
		return fmt.Errorf("invalid worktree HEAD")
	}
	commits, err := s.Git.CommitIDsWithPrefix(ctx, worktree, strings.ToLower(head[:8]))
	if err != nil {
		return err
	}
	if len(commits) != 1 || commits[0] != head {
		return fmt.Errorf("worktree HEAD %s does not uniquely match its selector", head)
	}
	return nil
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
		if err := s.validateCodeSelectorIdentity(ctx, worktree, status.Head); err != nil {
			return nil, err
		}
		trainID, kind, label := "", "", ""
		if filepath.Clean(info.Path) == filepath.Clean(project.Root) {
			kind, label = "main", "main"
		} else if strings.HasPrefix(info.Branch, "refs/heads/train/") {
			candidateID := strings.TrimPrefix(info.Branch, "refs/heads/train/")
			train, ok := managed[candidateID]
			if !ok {
				continue
			}
			runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, projectID, candidateID)
			var runtimeBinding *trainv2.RuntimeBinding
			if runtimeErr == nil {
				runtimeBinding = &runtime
			} else if !errors.Is(runtimeErr, os.ErrNotExist) {
				return nil, fmt.Errorf("read managed Train %s runtime: %w", candidateID, runtimeErr)
			} else if train.Status == model.TrainV2Running {
				return nil, fmt.Errorf("managed running Train %s has no server-owned runtime binding", candidateID)
			}
			expectedPath, pathErr := codeTrainWorktreePath(s.Config.StateDir, projectID, project, candidateID, runtimeBinding)
			if pathErr != nil {
				return nil, pathErr
			}
			if filepath.Clean(info.Path) != filepath.Clean(expectedPath) {
				return nil, fmt.Errorf("managed Train %s is bound to unexpected worktree path", candidateID)
			}
			trainID, kind, label = candidateID, "train", candidateID
			base, baseErr := codeTrainBase(train, status.Head)
			if baseErr != nil {
				return nil, baseErr
			}
			if base != status.Head {
				ancestor, ancestorErr := s.Git.IsAncestor(ctx, worktree.Root, base, status.Head)
				if ancestorErr != nil || !ancestor {
					return nil, fmt.Errorf("managed Train %s has an invalid authoritative base", candidateID)
				}
			}
			selector, selectorErr := codeSelector(trainID, status.Head)
			if selectorErr != nil {
				return nil, selectorErr
			}
			if _, exists := seen[selector]; exists {
				return nil, fmt.Errorf("ambiguous worktree selector %q", selector)
			}
			seen[selector] = struct{}{}
			candidates = append(candidates, codeWorktreeCandidate{
				localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{ProjectID: projectID, Worktree: selector, Dirty: !status.Clean, CurrentHead: status.Head}, ProjectWorktree: worktree, Kind: kind, TrainID: trainID, DiffBase: base},
				Label:           label,
			})
			continue
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
		candidates = append(candidates, codeWorktreeCandidate{
			localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{ProjectID: projectID, Worktree: selector, Dirty: !status.Clean, CurrentHead: status.Head}, ProjectWorktree: worktree, Kind: kind, DiffBase: status.Head},
			Label:           label,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CodeIdentity.Worktree < candidates[j].CodeIdentity.Worktree })
	return candidates, nil
}

func codeTrainBase(train model.TrainV2, currentHead string) (string, error) {
	base := ""
	setBase := func(value string) error {
		if model.ValidateCommitSHA(value) != nil {
			return fmt.Errorf("managed Train %s has an invalid authoritative base", train.ID)
		}
		if base != "" && base != value {
			return fmt.Errorf("managed Train %s has conflicting authoritative bases", train.ID)
		}
		base = value
		return nil
	}
	for _, item := range train.Items {
		if item.ActiveAttemptNumber > 0 && item.ActiveAttemptNumber <= uint64(len(item.Attempts)) {
			attempt := item.Attempts[item.ActiveAttemptNumber-1]
			if attempt.Status == model.TrainV2AttemptRunning {
				if err := setBase(attempt.StartHead); err != nil {
					return "", err
				}
			}
		}
		if item.SuccessfulAttemptNumber > 0 {
			if item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
				return "", fmt.Errorf("managed Train %s has an invalid successful Attempt", train.ID)
			}
			if err := setBase(item.Attempts[item.SuccessfulAttemptNumber-1].StartHead); err != nil {
				return "", err
			}
		}
	}
	if base == "" {
		return currentHead, nil
	}
	return base, nil
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
		if candidate.TrainID != "" && candidate.DiffBase == "" {
			return localCodeTarget{}, fmt.Errorf("worktree selector %q has no authoritative Train base", selector)
		}
		candidate.Live = live
		if candidate.TrainID != "" {
			ancestor, ancestorErr := s.Git.IsAncestor(ctx, candidate.ProjectWorktree.Root, candidate.DiffBase, candidate.CurrentHead)
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
			return localCodeTarget{}, &CodeSelectorError{Kind: CodeSelectorStale, Selector: selector, Current: candidate.CodeIdentity.Worktree}
		}
	}
	return localCodeTarget{}, &CodeSelectorError{Kind: CodeSelectorNotFound, Selector: selector}
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
		items = append(items, CodeWorktreeItem{Selector: candidate.CodeIdentity.Worktree, Kind: candidate.Kind, Dirty: candidate.Dirty, Head: candidate.CurrentHead, Label: candidate.Label, TrainID: candidate.TrainID})
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
	limit, err := PublicCollectionLimit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return CodeTreeResult{}, err
	}
	kind := codeCursorKind("code-tree", target, in.Path+"|"+in.Query+"|"+strconv.FormatBool(target.Live))
	if in.Cursor != "" {
		if err = pagination.ValidateOpaqueCursor(in.Cursor, kind); err != nil {
			return CodeTreeResult{}, err
		}
	}
	paths := make([]string, 0, limit)
	hasCursor := in.Cursor != ""
	hasMore, afterSeen := false, !hasCursor
	scanPaths := 0
	lastScannedPath := ""
	scanLimited := false
	walkErr := s.walkCodePaths(ctx, target, nil, in.Path, nil, nil, func(pathName string) (visitErr error) {
		scanPaths++
		lastScannedPath = pathName
		cursorFound := false
		defer func() {
			if visitErr == nil && scanPaths >= LocalCodeScanLookahead && !cursorFound && (in.Cursor == "" || afterSeen) {
				scanLimited = true
				visitErr = errCodeScanLimit
			}
		}()
		if hasCursor {
			if !afterSeen && pagination.OpaqueCursorMatches(in.Cursor, kind, pathName) {
				afterSeen = true
				cursorFound = true
				scanPaths = 0
				scanLimited = false
				return nil
			}
			if !afterSeen {
				return nil
			}
			if pagination.OpaqueCursorMatches(in.Cursor, kind, pathName) {
				return fmt.Errorf("ambiguous continuation cursor")
			}
		}
		if in.Path != "" && pathName != in.Path && !strings.HasPrefix(pathName, strings.TrimSuffix(in.Path, "/")+"/") {
			return nil
		}
		if in.Query != "" && !strings.Contains(pathName, in.Query) {
			return nil
		}
		if len(paths) == limit {
			hasMore = true
			return errCodePageDone
		}
		paths = append(paths, pathName)
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errCodePageDone) && !errors.Is(walkErr, errCodeScanLimit) {
		return CodeTreeResult{}, walkErr
	}
	if hasCursor && !afterSeen {
		if scanLimited {
			return CodeTreeResult{}, fmt.Errorf("continuation cursor requires a narrower scan")
		}
		return CodeTreeResult{}, fmt.Errorf("continuation cursor is no longer valid")
	}
	result := CodeTreeResult{CodeIdentity: target.CodeIdentity, Paths: paths, HasMore: hasMore}
	if scanLimited {
		result.HasMore = true
		result.NextCursor = pagination.EncodeFull(kind, lastScannedPath)
	} else if hasMore {
		result.NextCursor = pagination.EncodeFull(kind, paths[len(paths)-1])
	}
	return result, nil
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

func (s *Service) walkCodePaths(ctx context.Context, target localCodeTarget, paths []string, rootPath string, include, exclude []string, visit func(string) error) error {
	selected, err := validateLocalCodePaths(paths, false)
	if err != nil {
		return err
	}
	if err := validateCodePatterns(include); err != nil {
		return err
	}
	if err := validateCodePatterns(exclude); err != nil {
		return err
	}
	if len(selected) > 0 {
		for _, pathName := range selected {
			if codePathMatches(pathName, include, exclude) {
				if err := visit(pathName); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if target.Live {
		return s.Git.WalkWorkingTreeFiles(ctx, target.ProjectWorktree, rootPath, func(pathName string) error {
			if codePathMatches(pathName, include, exclude) {
				return visit(pathName)
			}
			return nil
		})
	}
	return s.Git.WalkTreeLocal(ctx, target.ProjectWorktree, target.CurrentHead, rootPath, func(pathName string) error {
		if codePathMatches(pathName, include, exclude) {
			return visit(pathName)
		}
		return nil
	})
}

func (s *Service) readCodeFile(ctx context.Context, target localCodeTarget, pathName string) (string, error) {
	if s.codeFileReader != nil {
		return s.codeFileReader(ctx, target, pathName)
	}
	if target.Live {
		return s.Git.ReadWorkingFile(ctx, target.ProjectWorktree, pathName)
	}
	return s.Git.ReadLocalFile(ctx, target.ProjectWorktree, target.CurrentHead, pathName)
}

func (s *Service) CodeRead(ctx context.Context, in CodeReadInput) (CodeReadResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.Worktree, in.Live)
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
	lines := strings.Split(content, "\n")
	start := in.StartLine
	if start == 0 {
		start = 1
	}
	kind := codeCursorKind("code-read", target, in.Path+"|"+strconv.FormatBool(target.Live))
	if in.Cursor != "" {
		keys := make([]string, len(lines)+1)
		for index := range keys {
			keys[index] = strconv.Itoa(index + 1)
		}
		resolved, resolveErr := pagination.Resolve(in.Cursor, kind, keys)
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
		result.NextCursor = pagination.Encode(kind, strconv.Itoa(end+1))
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
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.Worktree, in.Live)
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
	selectedPaths, err := validateLocalCodePaths(in.Paths, false)
	if err != nil {
		return CodeSearchResult{}, err
	}
	kind := codeCursorKind("code-search", target, in.Query+"|"+strings.Join(selectedPaths, "\x00")+"|"+strings.Join(in.Include, "\x00")+"|"+strings.Join(in.Exclude, "\x00")+"|"+strconv.FormatBool(target.Live))
	if in.Cursor != "" {
		if err := pagination.ValidateSearchCursor(in.Cursor, kind); err != nil {
			return CodeSearchResult{}, err
		}
	}
	result := CodeSearchResult{CodeIdentity: target.CodeIdentity, Matches: make([]CodeSearchMatch, 0, limit)}
	hasMore := false
	pathsScanned := 0
	lastScannedPath := ""
	scanLimited := false
	afterSeen := in.Cursor == ""
	walkErr := s.walkCodePaths(ctx, target, selectedPaths, "", in.Include, in.Exclude, func(pathName string) (visitErr error) {
		pathsScanned++
		lastScannedPath = pathName
		cursorFound := false
		defer func() {
			if visitErr == nil && pathsScanned >= LocalCodeScanLookahead && !cursorFound && (in.Cursor == "" || afterSeen) {
				scanLimited = true
				visitErr = errCodeScanLimit
			}
		}()
		cursorLine := 0
		if !afterSeen {
			if !pagination.SearchCursorPathMatches(in.Cursor, kind, pathName) {
				return nil
			}
			var cursorErr error
			cursorLine, cursorErr = pagination.DecodeSearchCursorLine(in.Cursor, kind, pathName)
			if cursorErr != nil {
				return cursorErr
			}
			if cursorLine == 0 {
				afterSeen = true
				cursorFound = true
				pathsScanned = 0
				scanLimited = false
				return nil
			}
		}
		data, readErr := s.readCodeFile(ctx, target, pathName)
		if readErr != nil {
			return readErr
		}
		if len(data) > LocalCodeMaxBytes {
			return fmt.Errorf("code search file exceeds bounded object limit")
		}
		if strings.IndexByte(data, 0) >= 0 {
			return nil
		}
		for lineNumber, line := range strings.Split(data, "\n") {
			if !strings.Contains(line, in.Query) {
				continue
			}
			if !afterSeen {
				if lineNumber+1 == cursorLine {
					afterSeen = true
					cursorFound = true
					pathsScanned = 0
					scanLimited = false
				}
				continue
			}
			if len(result.Matches) == limit {
				hasMore = true
				return errCodePageDone
			}
			snippet := line
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			result.Matches = append(result.Matches, CodeSearchMatch{Path: pathName, Line: lineNumber + 1, Snippet: snippet})
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errCodePageDone) && !errors.Is(walkErr, errCodeScanLimit) {
		return CodeSearchResult{}, walkErr
	}
	if in.Cursor != "" && !afterSeen {
		if scanLimited {
			return CodeSearchResult{}, fmt.Errorf("continuation cursor requires a narrower scan")
		}
		return CodeSearchResult{}, fmt.Errorf("continuation cursor is no longer valid")
	}
	result.PathsScanned = pathsScanned
	result.HasMore = hasMore
	result.Truncated = hasMore
	if scanLimited {
		result.HasMore = true
		result.Truncated = true
		result.NextCursor = pagination.EncodeSearchCursor(kind, lastScannedPath, 0)
	} else if hasMore {
		last := result.Matches[len(result.Matches)-1]
		result.NextCursor = pagination.EncodeSearchCursor(kind, last.Path, last.Line)
	}
	return result, nil
}

func (s *Service) CodeDiff(ctx context.Context, in CodeDiffInput) (CodeDiffResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.Worktree, in.Live)
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
	offset := int64(0)
	kind := codeCursorKind("code-diff", target, strings.Join(paths, "\x00")+"|"+strconv.FormatBool(target.Live))
	if in.Cursor != "" {
		var decodeErr error
		offset, decodeErr = pagination.DecodeOffset(in.Cursor, kind)
		if decodeErr != nil {
			return CodeDiffResult{}, decodeErr
		}
		if offset < 0 {
			return CodeDiffResult{}, fmt.Errorf("invalid code diff cursor")
		}
	}
	var diff string
	var hasMore bool
	if target.Live {
		diff, hasMore, err = s.Git.DiffWorkingFromBasePage(ctx, target.ProjectWorktree, target.DiffBase, paths, offset, maxBytes)
	} else {
		diff, hasMore, err = s.Git.DiffLocalCommitsPage(ctx, target.ProjectWorktree, target.DiffBase, target.CurrentHead, paths, offset, maxBytes)
	}
	if err != nil {
		return CodeDiffResult{}, err
	}
	result := CodeDiffResult{CodeIdentity: target.CodeIdentity, Paths: paths, Diff: diff, HasMore: hasMore, Truncated: hasMore}
	if hasMore {
		result.NextCursor = pagination.EncodeOffset(kind, offset+int64(len(diff)))
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
