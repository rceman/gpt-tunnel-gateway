package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const (
	LocalCodeMaxBytes      = 256 << 10
	LocalCodeMaxPaths      = 64
	LocalCodeMaxQueryBytes = 256
	LocalCodeMaxPatterns   = 32
	LocalCodeMaxScanPaths  = 4096
	LocalCodeScanLookahead = LocalCodeMaxScanPaths + 1
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
	ProjectID string `json:"-"`
	Query     string `json:"query"`
	Cursor    string `json:"cursor"`
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
	Pagination *CodePagination    `json:"_pagination,omitempty"`
}

type CodeTreeInput struct {
	ProjectID string `json:"-"`
	Worktree  string `json:"worktree"`
	Path      string `json:"path"`
	Query     string `json:"query"`
	Cursor    string `json:"cursor"`
	Live      bool   `json:"live"`
}

type CodeTreeResult struct {
	CodeIdentity
	Paths      []string        `json:"paths"`
	Pagination *CodePagination `json:"_pagination,omitempty"`
}

type CodeReadInput struct {
	ProjectID string `json:"-"`
	Worktree  string `json:"worktree"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line"` // semantic target, not a page-size control.
	Cursor    string `json:"cursor"`
	Live      bool   `json:"live"`
}

type CodeSearchInput struct {
	ProjectID string   `json:"-"`
	Worktree  string   `json:"worktree"`
	Query     string   `json:"query"`
	Paths     []string `json:"paths"`
	Include   []string `json:"include"`
	Exclude   []string `json:"exclude"`
	Cursor    string   `json:"cursor"`
	Live      bool     `json:"live"`
}

type CodeDiffInput struct {
	ProjectID string   `json:"-"`
	Worktree  string   `json:"worktree"`
	Paths     []string `json:"paths"`
	Cursor    string   `json:"cursor"`
	Live      bool     `json:"live"`
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
	Path       string          `json:"path"`
	StartLine  int             `json:"start_line"`
	EndLine    int             `json:"end_line"`
	TotalLines int             `json:"total_lines"`
	Content    string          `json:"content"`
	Pagination *CodePagination `json:"_pagination,omitempty"`
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
	Pagination   *CodePagination   `json:"_pagination,omitempty"`
}

type CodeDiffResult struct {
	CodeIdentity
	Paths      []string        `json:"paths"`
	Diff       string          `json:"diff"`
	Pagination *CodePagination `json:"_pagination,omitempty"`
}

type CodePagination struct {
	NextCursor string `json:"next_cursor"`
}

func codePagination(nextCursor string) *CodePagination {
	if nextCursor == "" {
		return nil
	}
	return &CodePagination{NextCursor: nextCursor}
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
	Label     string
	CreatedAt time.Time
	SortID    string
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
	case model.TrainV2Running, model.TrainV2Paused, model.TrainV2Blocked, model.TrainV2ReadyForIntegration:
		return true
	default:
		return false
	}
}

func codeWorktreeKindRank(kind string) int {
	switch kind {
	case "main":
		return 0
	case "hotfix":
		return 1
	case "train":
		return 2
	default:
		return 3
	}
}

func sortCodeWorktreeCandidates(candidates []codeWorktreeCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		leftRank, rightRank := codeWorktreeKindRank(candidates[i].Kind), codeWorktreeKindRank(candidates[j].Kind)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if !candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
		}
		if candidates[i].SortID != candidates[j].SortID {
			return candidates[i].SortID > candidates[j].SortID
		}
		return candidates[i].CodeIdentity.Worktree < candidates[j].CodeIdentity.Worktree
	})
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

func codeHotfixSelector(slug, head string) (string, error) {
	if err := model.ValidateTaskSlug(slug); err != nil {
		return "", err
	}
	if len(head) < 8 || model.ValidateCommitSHA(head) != nil {
		return "", fmt.Errorf("invalid worktree HEAD")
	}
	return "WT-FIX-" + slug + "-" + strings.ToLower(head[:8]), nil
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
	trains, err := s.codeTrainRecords(ctx, projectID)
	if err != nil && !IsNotFound(err) {
		return nil, fmt.Errorf("read managed Train worktrees: %w", err)
	}
	mainStatus, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("read main worktree status: %w", err)
	}
	worktreeInventory, err := s.Git.LoadWorktreeInventory(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("read Git worktree inventory: %w", err)
	}
	mainHead := mainStatus.Head
	managed := make(map[string]model.TrainV2, len(trains))
	for _, train := range trains {
		if train.ProjectID == projectID && activeCodeTrainStatus(train.Status) && train.Historical == nil {
			managed[train.ID] = train
		}
	}
	candidates := make([]codeWorktreeCandidate, 0, len(managed)+1)
	seen := make(map[string]struct{})
	addCandidate := func(worktree config.ProjectConfig, status gitx.WorktreeStatus, kind, label, trainID, diffBase string, createdAt time.Time, sortID string) error {
		if err := s.validateCodeSelectorIdentity(ctx, worktree, status.Head); err != nil {
			return err
		}
		selector, selectorErr := codeSelector(trainID, status.Head)
		if selectorErr != nil {
			return selectorErr
		}
		if _, exists := seen[selector]; exists {
			return fmt.Errorf("ambiguous worktree selector %q", selector)
		}
		seen[selector] = struct{}{}
		candidates = append(candidates, codeWorktreeCandidate{
			localCodeTarget: localCodeTarget{CodeIdentity: CodeIdentity{ProjectID: projectID, Worktree: selector, Dirty: !status.Clean, CurrentHead: status.Head}, ProjectWorktree: worktree, Kind: kind, TrainID: trainID, DiffBase: diffBase},
			Label:           label, CreatedAt: createdAt, SortID: sortID,
		})
		return nil
	}
	if err := addCandidate(project, mainStatus, "main", "main", "", mainStatus.Head, time.Time{}, "main"); err != nil {
		return nil, err
	}
	hotfixes, err := s.Git.ListHotfixIdentities(s.Config.StateDir, projectID)
	if err != nil {
		return nil, fmt.Errorf("read managed hotfix identities: %w", err)
	}
	for _, identity := range hotfixes {
		if identity.CreatedAt.IsZero() {
			continue
		}
		worktree, resolveErr := s.Git.ResolveHotfixWorktreeFromInventory(worktreeInventory, s.Config.StateDir, projectID, identity.HotfixRef)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve managed hotfix %s worktree: %w", identity.HotfixRef, resolveErr)
		}
		status, statusErr := s.Git.WorktreeStatus(ctx, worktree)
		if statusErr != nil {
			return nil, fmt.Errorf("read managed hotfix %s worktree status: %w", identity.HotfixRef, statusErr)
		}
		merged, ancestorErr := s.Git.IsAncestor(ctx, project.Root, status.Head, mainHead)
		if ancestorErr != nil {
			return nil, fmt.Errorf("check hotfix %s merge state: %w", identity.HotfixRef, ancestorErr)
		}
		if merged {
			continue
		}
		slug := strings.TrimPrefix(identity.HotfixRef, "refs/heads/hotfix/")
		if err := addCandidate(worktree, status, "hotfix", slug, "", identity.BaseSHA, identity.CreatedAt, slug); err != nil {
			return nil, err
		}
	}
	trainIDs := make([]string, 0, len(managed))
	for candidateID := range managed {
		trainIDs = append(trainIDs, candidateID)
	}
	sort.Strings(trainIDs)
	for _, candidateID := range trainIDs {
		train := managed[candidateID]
		runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, projectID, candidateID)
		if runtimeErr != nil {
			return nil, fmt.Errorf("read managed Train %s runtime: %w", candidateID, runtimeErr)
		}
		runtimeBinding := &runtime
		expectedPath, pathErr := codeTrainWorktreePath(s.Config.StateDir, projectID, project, candidateID, runtimeBinding)
		if pathErr != nil {
			return nil, pathErr
		}
		worktree, resolveErr := worktreeInventory.Resolve("refs/heads/train/" + candidateID)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve managed Train %s worktree: %w", candidateID, resolveErr)
		}
		if filepath.Clean(worktree.Root) != filepath.Clean(expectedPath) {
			return nil, fmt.Errorf("managed Train %s is bound to unexpected worktree path", candidateID)
		}
		status, statusErr := s.Git.WorktreeStatus(ctx, worktree)
		if statusErr != nil {
			return nil, fmt.Errorf("read managed Train %s worktree status: %w", candidateID, statusErr)
		}
		merged, ancestorErr := s.Git.IsAncestor(ctx, project.Root, status.Head, mainHead)
		if ancestorErr != nil {
			return nil, fmt.Errorf("check Train %s merge state: %w", candidateID, ancestorErr)
		}
		if merged {
			continue
		}
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
		if err := addCandidate(worktree, status, "train", candidateID, candidateID, base, train.CreatedAt, candidateID); err != nil {
			return nil, err
		}
	}
	sortCodeWorktreeCandidates(candidates)
	return candidates, nil
}

func codeTrainBase(train model.TrainV2, currentHead string) (string, error) {
	if len(train.Items) == 0 || len(train.Items[0].Attempts) == 0 {
		if train.Status == model.TrainV2Planned {
			return currentHead, nil
		}
		return "", fmt.Errorf("managed Train %s has no canonical Shared Train base", train.ID)
	}
	// The first Attempt of the first Shared Train item is the canonical lane
	// base. Later item Attempts start from intermediate heads and must not be
	// mistaken for separate Train bases.
	base := train.Items[0].Attempts[0].StartHead
	if model.ValidateCommitSHA(base) != nil {
		return "", fmt.Errorf("managed Train %s has an invalid canonical Shared Train base", train.ID)
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
		if (candidate.Kind == "train" || candidate.Kind == "hotfix") && candidate.DiffBase == "" {
			return localCodeTarget{}, fmt.Errorf("worktree selector %q has no authoritative Train base", selector)
		}
		candidate.Live = live
		if candidate.Kind == "train" || candidate.Kind == "hotfix" {
			ancestor, ancestorErr := s.Git.IsAncestor(ctx, candidate.ProjectWorktree.Root, candidate.DiffBase, candidate.CurrentHead)
			if ancestorErr != nil || !ancestor {
				return localCodeTarget{}, fmt.Errorf("worktree selector %q has an invalid authoritative Train base", selector)
			}
		}
		return candidate.localCodeTarget, nil
	}
	if kind, number, prefix, parseErr := parseCodeSelector(selector); parseErr == nil {
		for _, candidate := range candidates {
			if candidate.Kind != kind || (kind == "train" && number != candidateTrainNumber(candidate.TrainID)) || (kind == "hotfix" && candidate.SortID != prefix) {
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
	if strings.HasPrefix(selector, "WT-FIX-") {
		rest := strings.TrimPrefix(selector, "WT-FIX-")
		separator := strings.LastIndexByte(rest, '-')
		if separator < 1 || separator == len(rest)-1 {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		slug, prefix := rest[:separator], rest[separator+1:]
		if model.ValidateTaskSlug(slug) != nil || !validSelectorPrefix(prefix) {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		return "hotfix", 0, slug, nil
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
	kind := "code-worktree|" + in.ProjectID + "|" + query
	if len(items) == 0 {
		result := CodeWorktreeResult{Items: items}
		fits, fitErr := codePageFits(result)
		if fitErr != nil {
			return CodeWorktreeResult{}, fitErr
		}
		if fits {
			return result, nil
		}
		return CodeWorktreeResult{}, fmt.Errorf("code worktree result exceeds %d tokenizer tokens", CodePageTokenBudget)
	}
	page, nextCursor, pageErr := codeWorktreePage(kind, items, in.Cursor)
	if pageErr != nil {
		return CodeWorktreeResult{}, pageErr
	}
	return CodeWorktreeResult{Items: page, Pagination: codePagination(nextCursor)}, nil
}

func codeWorktreePage(kind string, items []CodeWorktreeItem, rawCursor string) ([]CodeWorktreeItem, string, error) {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Selector)
	}
	after, err := pagination.Resolve(rawCursor, kind, keys)
	if err != nil {
		return nil, "", err
	}
	start := 0
	if after != "" {
		for index, item := range items {
			if item.Selector == after {
				start = index + 1
				break
			}
		}
		if start == 0 {
			return nil, "", fmt.Errorf("continuation cursor is no longer valid")
		}
	}
	page := make([]CodeWorktreeItem, 0, len(items)-start)
	for index := start; index < len(items); index++ {
		candidate := append(append([]CodeWorktreeItem{}, page...), items[index])
		nextCursor := ""
		if index+1 < len(items) {
			nextCursor = pagination.Encode(kind, items[index].Selector)
		}
		fits, fitErr := codePageFits(CodeWorktreeResult{Items: candidate, Pagination: codePagination(nextCursor)})
		if fitErr != nil {
			return nil, "", fitErr
		}
		if !fits {
			if len(page) == 0 {
				return nil, "", fmt.Errorf("code worktree item exceeds %d tokenizer tokens", CodePageTokenBudget)
			}
			return page, pagination.Encode(kind, page[len(page)-1].Selector), nil
		}
		page = candidate
	}
	return page, "", nil
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
	kind := codeCursorKind("code-tree", target, in.Path+"|"+in.Query+"|"+strconv.FormatBool(target.Live))
	if in.Cursor != "" {
		if err = pagination.ValidateOpaqueCursor(in.Cursor, kind); err != nil {
			return CodeTreeResult{}, err
		}
	}
	paths := make([]string, 0, LocalCodeMaxScanPaths)
	hasCursor := in.Cursor != ""
	afterSeen := !hasCursor
	scanPaths := 0
	walkErr := s.walkCodePaths(ctx, target, nil, in.Path, nil, nil, func(pathName string) (visitErr error) {
		scanPaths++
		cursorFound := false
		defer func() {
			if visitErr == nil && scanPaths >= LocalCodeScanLookahead && !cursorFound && (in.Cursor == "" || afterSeen) {
				visitErr = errCodeScanLimit
			}
		}()
		if hasCursor {
			if !afterSeen && pagination.OpaqueCursorMatches(in.Cursor, kind, pathName) {
				afterSeen = true
				cursorFound = true
				scanPaths = 0
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
		paths = append(paths, pathName)
		return nil
	})
	if errors.Is(walkErr, errCodeScanLimit) {
		return CodeTreeResult{}, fmt.Errorf("code tree scan exceeded bounded work; narrow the path or query")
	}
	if walkErr != nil && !errors.Is(walkErr, errCodePageDone) {
		return CodeTreeResult{}, walkErr
	}
	if hasCursor && !afterSeen {
		return CodeTreeResult{}, fmt.Errorf("continuation cursor is no longer valid")
	}
	pageSize, fitErr := largestCodePageSize(len(paths), func(size int) (bool, error) {
		pageCursor := ""
		if size < len(paths) {
			pageCursor = pagination.EncodeFull(kind, paths[size-1])
		}
		return codePageFits(CodeTreeResult{CodeIdentity: target.CodeIdentity, Paths: paths[:size], Pagination: codePagination(pageCursor)})
	})
	if fitErr != nil {
		return CodeTreeResult{}, fitErr
	}
	pageCursor := ""
	if pageSize < len(paths) {
		pageCursor = pagination.EncodeFull(kind, paths[pageSize-1])
	}
	return CodeTreeResult{CodeIdentity: target.CodeIdentity, Paths: paths[:pageSize], Pagination: codePagination(pageCursor)}, nil
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
	count := len(lines) - start + 1
	if start < 1 || count < 1 {
		return CodeReadResult{}, fmt.Errorf("invalid code line range")
	}
	if start > len(lines)+1 {
		return CodeReadResult{}, fmt.Errorf("code read start_line exceeds file")
	}
	end := start + count - 1
	if end > len(lines) {
		end = len(lines)
	}
	pageSize, fitErr := largestCodePageSize(count, func(pageSize int) (bool, error) {
		pageEnd := start + pageSize - 1
		pageContinuation := pageEnd < len(lines)
		pageCursor := ""
		if pageContinuation {
			pageCursor = pagination.Encode(kind, strconv.Itoa(pageEnd+1))
		}
		candidate := CodeReadResult{
			CodeIdentity: target.CodeIdentity, Path: in.Path, StartLine: start, EndLine: pageEnd,
			TotalLines: len(lines), Content: strings.Join(lines[start-1:pageEnd], "\n"),
			Pagination: codePagination(pageCursor),
		}
		return codePageFits(candidate)
	})
	if fitErr != nil {
		return CodeReadResult{}, fitErr
	}
	if pageSize > 0 {
		pageEnd := start + pageSize - 1
		pageContinuation := pageEnd < len(lines)
		pageCursor := ""
		if pageContinuation {
			pageCursor = pagination.Encode(kind, strconv.Itoa(pageEnd+1))
		}
		return CodeReadResult{
			CodeIdentity: target.CodeIdentity, Path: in.Path, StartLine: start, EndLine: pageEnd,
			TotalLines: len(lines), Content: strings.Join(lines[start-1:pageEnd], "\n"),
			Pagination: codePagination(pageCursor),
		}, nil
	}
	return CodeReadResult{}, fmt.Errorf("code read line exceeds %d tokenizer tokens", CodePageTokenBudget)
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
	result := CodeSearchResult{CodeIdentity: target.CodeIdentity, Matches: make([]CodeSearchMatch, 0)}
	continuation := false
	pathsScanned := 0
	afterSeen := in.Cursor == ""
	walkErr := s.walkCodePaths(ctx, target, selectedPaths, "", in.Include, in.Exclude, func(pathName string) (visitErr error) {
		pathsScanned++
		cursorFound := false
		defer func() {
			if visitErr == nil && pathsScanned >= LocalCodeScanLookahead && !cursorFound && (in.Cursor == "" || afterSeen) {
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
				}
				continue
			}
			snippet := line
			if len(snippet) > 240 {
				snippet = snippet[:240]
			}
			candidate := CodeSearchResult{
				CodeIdentity: result.CodeIdentity, PathsScanned: pathsScanned,
				Matches: append(append([]CodeSearchMatch(nil), result.Matches...), CodeSearchMatch{Path: pathName, Line: lineNumber + 1, Snippet: snippet}),
			}
			fits, fitErr := codePageFits(candidate)
			if fitErr != nil {
				return fitErr
			}
			if !fits {
				continuation = len(result.Matches) > 0
				if !continuation {
					return fmt.Errorf("code search match exceeds %d tokenizer tokens", CodePageTokenBudget)
				}
				return errCodePageDone
			}
			result.Matches = append(result.Matches, CodeSearchMatch{Path: pathName, Line: lineNumber + 1, Snippet: snippet})
		}
		return nil
	})
	if errors.Is(walkErr, errCodeScanLimit) {
		return CodeSearchResult{}, fmt.Errorf("code search scan exceeded bounded work; narrow the paths or patterns")
	}
	if walkErr != nil && !errors.Is(walkErr, errCodePageDone) {
		return CodeSearchResult{}, walkErr
	}
	if in.Cursor != "" && !afterSeen {
		return CodeSearchResult{}, fmt.Errorf("continuation cursor is no longer valid")
	}
	result.PathsScanned = pathsScanned
	resultCursor := ""
	if continuation {
		last := result.Matches[len(result.Matches)-1]
		resultCursor = pagination.EncodeSearchCursor(kind, last.Path, last.Line)
	}
	pageSize, fitErr := largestCodePageSize(len(result.Matches), func(size int) (bool, error) {
		pageCursor := resultCursor
		if size < len(result.Matches) {
			last := result.Matches[size-1]
			pageCursor = pagination.EncodeSearchCursor(kind, last.Path, last.Line)
		}
		return codePageFits(CodeSearchResult{
			CodeIdentity: result.CodeIdentity, PathsScanned: result.PathsScanned, Matches: result.Matches[:size],
			Pagination: codePagination(pageCursor),
		})
	})
	if fitErr != nil {
		return CodeSearchResult{}, fitErr
	}
	if pageSize > 0 {
		pageCursor := resultCursor
		if pageSize < len(result.Matches) {
			last := result.Matches[pageSize-1]
			pageCursor = pagination.EncodeSearchCursor(kind, last.Path, last.Line)
		}
		return CodeSearchResult{CodeIdentity: result.CodeIdentity, PathsScanned: result.PathsScanned, Matches: result.Matches[:pageSize], Pagination: codePagination(pageCursor)}, nil
	}
	if len(result.Matches) == 0 {
		result.Pagination = codePagination(resultCursor)
		fits, fitErr := codePageFits(result)
		if fitErr != nil {
			return CodeSearchResult{}, fitErr
		}
		if fits {
			return result, nil
		}
	}
	return CodeSearchResult{}, fmt.Errorf("code search match exceeds %d tokenizer tokens", CodePageTokenBudget)
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
	pageLines := make([]string, 0)
	nextOffset := int64(-1)
	visit := func(lineOffset int64, line []byte) error {
		candidateLines := append(append([]string(nil), pageLines...), string(line))
		candidate := CodeDiffResult{
			CodeIdentity: target.CodeIdentity,
			Paths:        paths,
			Diff:         strings.Join(candidateLines, ""),
			Pagination:   codePagination(pagination.EncodeOffset(kind, lineOffset+1)),
		}
		fits, fitErr := codePageFits(candidate)
		if fitErr != nil {
			return fitErr
		}
		if !fits {
			if len(pageLines) == 0 {
				return fmt.Errorf("code diff line exceeds %d tokenizer tokens", CodePageTokenBudget)
			}
			nextOffset = lineOffset
			return gitx.ErrStreamLimit
		}
		pageLines = append(pageLines, string(line))
		return nil
	}
	var continuation bool
	if target.Live {
		continuation, err = s.Git.VisitDiffWorkingFromBase(ctx, target.ProjectWorktree, target.DiffBase, paths, offset, visit)
	} else {
		continuation, err = s.Git.VisitDiffLocalCommits(ctx, target.ProjectWorktree, target.DiffBase, target.CurrentHead, paths, offset, visit)
	}
	if err != nil {
		return CodeDiffResult{}, err
	}
	if continuation && nextOffset < 0 {
		return CodeDiffResult{}, fmt.Errorf("code diff stream stopped without a continuation offset")
	}
	if len(pageLines) > 0 {
		pageCursor := ""
		if continuation {
			pageCursor = pagination.EncodeOffset(kind, nextOffset)
		}
		return CodeDiffResult{CodeIdentity: target.CodeIdentity, Paths: paths, Diff: strings.Join(pageLines, ""), Pagination: codePagination(pageCursor)}, nil
	}
	if !continuation {
		result := CodeDiffResult{CodeIdentity: target.CodeIdentity, Paths: paths}
		fits, fitErr := codePageFits(result)
		if fitErr != nil {
			return CodeDiffResult{}, fitErr
		}
		if fits {
			return result, nil
		}
	}
	return CodeDiffResult{}, fmt.Errorf("code diff line exceeds %d tokenizer tokens", CodePageTokenBudget)
}
