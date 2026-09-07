package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

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
		return CodeDiffResult{
			CodeIdentity: target.CodeIdentity,
			Paths:        paths,
			Diff:         strings.Join(pageLines, ""),
			Pagination:   codePagination(pageCursor),
		}, nil
	}
	if !continuation {
		result := CodeDiffResult{
			CodeIdentity: target.CodeIdentity,
			Paths:        paths,
		}
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
