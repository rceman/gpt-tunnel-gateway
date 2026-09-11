package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func (s *Service) CodeRead(ctx context.Context, in CodeReadInput) (CodeReadResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.Worktree, in.Live)
	if err != nil {
		return CodeReadResult{}, err
	}
	if err := model.ValidateRelativePath(in.Path); err != nil {
		return CodeReadResult{}, err
	}
	if in.Cursor != "" && in.LineCount != nil {
		return CodeReadResult{}, fmt.Errorf("code read cursor cannot be combined with line_count")
	}
	if in.LineCount != nil && *in.LineCount < 1 {
		return CodeReadResult{}, fmt.Errorf("code read line_count must be positive")
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
	if start < 1 {
		return CodeReadResult{}, fmt.Errorf("invalid code line range")
	}
	kind := codeCursorKind("code-read", target, in.Path+"|"+strconv.FormatBool(target.Live))
	boundedRange := in.LineCount != nil
	end := len(lines)
	if boundedRange {
		if start > len(lines) {
			return CodeReadResult{}, fmt.Errorf("code read start_line exceeds file")
		}
		remaining := len(lines) - start + 1
		if *in.LineCount < remaining {
			end = start + *in.LineCount - 1
		}
	}
	if in.Cursor != "" {
		var rangeErr error
		start, end, rangeErr = pagination.DecodeRangeCursor(in.Cursor, kind)
		if rangeErr == nil {
			boundedRange = true
		} else {
			keys := make([]string, len(lines)+1)
			for index := range keys {
				keys[index] = strconv.Itoa(index + 1)
			}
			resolved, resolveErr := pagination.Resolve(in.Cursor, kind, keys)
			if resolveErr != nil {
				return CodeReadResult{}, resolveErr
			}
			parts := strings.Split(resolved, ":")
			if len(parts) == 3 && parts[0] == "range" {
				start, err = strconv.Atoi(parts[1])
				if err == nil {
					end, err = strconv.Atoi(parts[2])
					boundedRange = true
				}
			} else {
				start, err = strconv.Atoi(resolved)
			}
			if err != nil {
				return CodeReadResult{}, fmt.Errorf("invalid code read cursor")
			}
			if !boundedRange {
				end = len(lines)
			}
		}
	}
	if start < 1 || start > len(lines) || end < start {
		return CodeReadResult{}, fmt.Errorf("invalid code line range")
	}
	if end > len(lines) {
		end = len(lines)
	}
	fileHash := codeFileHash(content)
	readIdentity := target.CodeIdentity
	readIdentity.CurrentHead = target.CurrentHead[:8]
	count := end - start + 1
	encodeCursor := func(next int) string {
		if boundedRange {
			return pagination.EncodeServerCursor(kind, fmt.Sprintf("range:%d:%d", next, end))
		}
		return pagination.Encode(kind, strconv.Itoa(next))
	}
	pageSize, fitErr := largestCodePageSize(count, func(pageSize int) (bool, error) {
		pageEnd := start + pageSize - 1
		pageContinuation := pageEnd < end
		pageCursor := ""
		if pageContinuation {
			pageCursor = encodeCursor(pageEnd + 1)
		}
		candidate := CodeReadResult{
			CodeIdentity: readIdentity,
			Path:         in.Path,
			StartLine:    start,
			EndLine:      pageEnd,
			TotalLines:   len(lines),
			Content:      strings.Join(lines[start-1:pageEnd], "\n"),
			FileHash:     fileHash,
			Pagination:   codePagination(pageCursor),
		}
		return codePageFits(candidate)
	})
	if fitErr != nil {
		return CodeReadResult{}, fitErr
	}
	if pageSize > 0 {
		pageEnd := start + pageSize - 1
		pageContinuation := pageEnd < end
		pageCursor := ""
		if pageContinuation {
			pageCursor = encodeCursor(pageEnd + 1)
		}
		return CodeReadResult{
			CodeIdentity: readIdentity,
			Path:         in.Path,
			StartLine:    start,
			EndLine:      pageEnd,
			TotalLines:   len(lines),
			Content:      strings.Join(lines[start-1:pageEnd], "\n"),
			FileHash:     fileHash,
			Pagination:   codePagination(pageCursor),
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
	if in.ContextLines < 0 || in.ContextLines > 3 {
		return CodeSearchResult{}, fmt.Errorf("context_lines must be between 0 and 3")
	}
	selectedPaths, err := validateLocalCodePaths(in.Paths, false)
	if err != nil {
		return CodeSearchResult{}, err
	}
	kind := codeCursorKind("code-search", target, in.Query+"|"+strings.Join(selectedPaths, "\x00")+"|"+strings.Join(in.Include, "\x00")+"|"+strings.Join(in.Exclude, "\x00")+"|"+strconv.Itoa(in.ContextLines)+"|"+strconv.FormatBool(target.Live))
	compactCursor := false
	if in.Cursor != "" {
		if _, compactCursor = pagination.ResolveServerCursor(in.Cursor, kind); !compactCursor {
			if err := pagination.ValidateSearchCursor(in.Cursor, kind); err != nil {
				return CodeSearchResult{}, err
			}
		}
	}
	result := CodeSearchResult{
		CodeIdentity: target.CodeIdentity,
		Matches:      make([]CodeSearchMatch, 0),
	}
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
		if !afterSeen && !compactCursor {
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
		lines := strings.Split(data, "\n")
		for lineNumber, line := range lines {
			if !strings.Contains(line, in.Query) {
				continue
			}
			if !afterSeen && compactCursor {
				if key, _ := pagination.ResolveServerCursor(in.Cursor, kind); key == pathName+"|"+strconv.Itoa(lineNumber+1) {
					afterSeen = true
					cursorFound = true
					pathsScanned = 0
				}
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
			snippet := boundedSearchSnippet(lines, lineNumber, in.ContextLines, in.Query)
			candidate := CodeSearchResult{
				CodeIdentity: result.CodeIdentity,
				PathsScanned: pathsScanned,
				Matches: append(append([]CodeSearchMatch(nil), result.Matches...), CodeSearchMatch{
					Path:    pathName,
					Line:    lineNumber + 1,
					Snippet: snippet,
				}),
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
			result.Matches = append(result.Matches, CodeSearchMatch{
				Path:    pathName,
				Line:    lineNumber + 1,
				Snippet: snippet,
			})
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
		resultCursor = pagination.EncodeServerCursor(kind, last.Path+"|"+strconv.Itoa(last.Line))
	}
	pageSize, fitErr := largestCodePageSize(len(result.Matches), func(size int) (bool, error) {
		pageCursor := resultCursor
		if size < len(result.Matches) {
			last := result.Matches[size-1]
			pageCursor = pagination.EncodeServerCursor(kind, last.Path+"|"+strconv.Itoa(last.Line))
		}
		return codePageFits(CodeSearchResult{
			CodeIdentity: result.CodeIdentity,
			PathsScanned: result.PathsScanned,
			Matches:      result.Matches[:size],
			Pagination:   codePagination(pageCursor),
		})
	})
	if fitErr != nil {
		return CodeSearchResult{}, fitErr
	}
	if pageSize > 0 {
		pageCursor := resultCursor
		if pageSize < len(result.Matches) {
			last := result.Matches[pageSize-1]
			pageCursor = pagination.EncodeServerCursor(kind, last.Path+"|"+strconv.Itoa(last.Line))
		}
		return CodeSearchResult{
			CodeIdentity: result.CodeIdentity,
			PathsScanned: result.PathsScanned,
			Matches:      result.Matches[:pageSize],
			Pagination:   codePagination(pageCursor),
		}, nil
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
