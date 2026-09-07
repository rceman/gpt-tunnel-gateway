package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
	StartLine int    `json:"start_line"` // semantic one-based range start.
	LineCount *int   `json:"line_count,omitempty"`
	Cursor    string `json:"cursor"`
	Live      bool   `json:"live"`
}

type CodeSearchInput struct {
	ProjectID    string   `json:"-"`
	Worktree     string   `json:"worktree"`
	Query        string   `json:"query"`
	Paths        []string `json:"paths"`
	Include      []string `json:"include"`
	Exclude      []string `json:"exclude"`
	ContextLines int      `json:"context_lines"`
	Cursor       string   `json:"cursor"`
	Live         bool     `json:"live"`
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
	FileHash   string          `json:"file_hash"`
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

func boundedSearchSnippet(lines []string, matchLine, contextLines int, query string) string {
	start := matchLine - contextLines
	if start < 0 {
		start = 0
	}
	end := matchLine + contextLines + 1
	if end > len(lines) {
		end = len(lines)
	}
	matched := lines[matchLine]
	if len(matched) >= 240 {
		matchAt := strings.Index(matched, query)
		if matchAt < 0 || len(query) >= 240 {
			return matched[:240]
		}
		matchStart := matchAt
		if matchStart+240 > len(matched) {
			matchStart = len(matched) - 240
		}
		return matched[matchStart : matchStart+240]
	}
	remaining := 240 - len(matched)
	before := strings.Join(lines[start:matchLine], "\n")
	after := strings.Join(lines[matchLine+1:end], "\n")
	separatorCount := 0
	if before != "" {
		separatorCount++
	}
	if after != "" {
		separatorCount++
	}
	remaining -= separatorCount
	if remaining < 0 {
		remaining = 0
	}
	beforeTake := len(before)
	if beforeTake > remaining/2 {
		beforeTake = remaining / 2
	}
	afterTake := len(after)
	if afterTake > remaining-beforeTake {
		afterTake = remaining - beforeTake
	}
	parts := make([]string, 0, 3)
	if beforeTake > 0 {
		parts = append(parts, before[len(before)-beforeTake:])
	}
	parts = append(parts, matched)
	if afterTake > 0 {
		parts = append(parts, after[:afterTake])
	}
	return strings.Join(parts, "\n")
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
