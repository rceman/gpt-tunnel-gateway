package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func (s *Service) CodeWorktree(ctx context.Context, in CodeWorktreeInput) (CodeWorktreeResult, error) {
	query := strings.TrimSpace(in.Query)
	if len(query) > LocalCodeMaxQueryBytes || strings.ContainsAny(query, "\x00\r\n") {
		return CodeWorktreeResult{}, fmt.Errorf("invalid worktree query")
	}
	kind := "code-worktree|" + in.ProjectID + "|" + query
	cursorFound := in.Cursor == ""
	page := make([]CodeWorktreeItem, 0)
	nextCursor := ""
	pageFull := errors.New("code worktree page full")
	_, streamErr := s.codeWorktreeCandidatesStream(ctx, in.ProjectID, func(candidate codeWorktreeCandidate) error {
		if query != "" && !strings.Contains(candidate.CodeIdentity.Worktree, query) && !strings.Contains(candidate.Label, query) && !strings.Contains(candidate.TrainID, query) {
			return nil
		}
		if !cursorFound {
			if codeWorktreeCursorMatches(in.Cursor, kind, candidate.CodeIdentity.Worktree) {
				cursorFound = true
			}
			return nil
		}
		item := CodeWorktreeItem{
			Selector: candidate.CodeIdentity.Worktree,
			Kind:     candidate.Kind,
			Dirty:    candidate.Dirty,
			Head:     candidate.CurrentHead,
			Label:    candidate.Label,
			TrainID:  candidate.TrainID,
		}
		trial := append(append([]CodeWorktreeItem{}, page...), item)
		candidateCursor := ""
		if len(trial) > 0 {
			candidateCursor = pagination.Encode(kind, item.Selector)
		}
		fits, fitErr := codePageFits(CodeWorktreeResult{Items: trial, Pagination: codePagination(candidateCursor)})
		if fitErr != nil {
			return fitErr
		}
		if !fits {
			if len(page) == 0 {
				return fmt.Errorf("code worktree item exceeds %d tokenizer tokens", CodePageTokenBudget)
			}
			nextCursor = pagination.Encode(kind, page[len(page)-1].Selector)
			return pageFull
		}
		page = trial
		return nil
	})
	if streamErr != nil && !errors.Is(streamErr, pageFull) {
		return CodeWorktreeResult{}, streamErr
	}
	if in.Cursor != "" && !cursorFound {
		return CodeWorktreeResult{}, fmt.Errorf("continuation cursor is no longer valid")
	}
	if nextCursor == "" {
		result := CodeWorktreeResult{Items: page}
		fits, fitErr := codePageFits(result)
		if fitErr != nil {
			return CodeWorktreeResult{}, fitErr
		}
		if !fits {
			return CodeWorktreeResult{}, fmt.Errorf("code worktree result exceeds %d tokenizer tokens", CodePageTokenBudget)
		}
	}
	return CodeWorktreeResult{
		Items:      page,
		Pagination: codePagination(nextCursor),
	}, nil
}

func codeWorktreeCursorMatches(raw, kind, selector string) bool {
	if raw == "" {
		return true
	}
	matched, err := pagination.Resolve(raw, kind, []string{selector})
	return err == nil && matched == selector
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
		fits, fitErr := codePageFits(CodeWorktreeResult{
			Items:      candidate,
			Pagination: codePagination(nextCursor),
		})
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
		return codePageFits(CodeTreeResult{
			CodeIdentity: target.CodeIdentity,
			Paths:        paths[:size],
			Pagination:   codePagination(pageCursor),
		})
	})
	if fitErr != nil {
		return CodeTreeResult{}, fitErr
	}
	pageCursor := ""
	if pageSize < len(paths) {
		pageCursor = pagination.EncodeFull(kind, paths[pageSize-1])
	}
	return CodeTreeResult{
		CodeIdentity: target.CodeIdentity,
		Paths:        paths[:pageSize],
		Pagination:   codePagination(pageCursor),
	}, nil
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
	if s.codePathWalker != nil {
		return s.codePathWalker(ctx, target, paths, rootPath, include, exclude, visit)
	}
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

func codeFileHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])[:8]
}
