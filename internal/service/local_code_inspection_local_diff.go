package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func validCodeDiffBase8(base string) bool {
	if len(base) != 8 {
		return false
	}
	for _, r := range base {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (s *Service) CodeDiff(ctx context.Context, in CodeDiffInput) (CodeDiffResult, error) {
	target, err := s.resolveLocalCodeTarget(ctx, in.ProjectID, in.Worktree, in.Live)
	if err != nil {
		return CodeDiffResult{}, err
	}
	if in.Base != "" {
		if !validCodeDiffBase8(in.Base) || target.Kind != "task" || target.TaskID == "" {
			return CodeDiffResult{}, fmt.Errorf("code diff base must be a server-authorized sha8 for a Task worktree")
		}
		state, found, stateErr := s.readExecutionForMutation(ctx, target.ProjectID, target.TaskID)
		if stateErr != nil || !found || state.Worktree != target.Worktree {
			if stateErr != nil {
				return CodeDiffResult{}, stateErr
			}
			return CodeDiffResult{}, fmt.Errorf("code diff base is not bound to the current Task worktree")
		}
		phase, comparisonBase, selectErr := s.taskExecutionReviewSelection(ctx, target.ProjectID, target.TaskID, state.Stage, state)
		if selectErr != nil {
			return CodeDiffResult{}, selectErr
		}
		if phase.Head != target.CurrentHead || strings.ToLower(comparisonBase[:8]) != in.Base {
			return CodeDiffResult{}, fmt.Errorf("code diff base is not authorized for this submission")
		}
		target.DiffBase = comparisonBase
	}
	if model.ValidateCommitSHA(target.DiffBase) != nil {
		return CodeDiffResult{}, fmt.Errorf("code diff has no valid comparison base")
	}
	diffBase8 := strings.ToLower(target.DiffBase[:8])
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
			key, ok := pagination.ResolveServerCursor(in.Cursor, kind)
			if !ok {
				return CodeDiffResult{}, decodeErr
			}
			offset, decodeErr = strconv.ParseInt(key, 10, 64)
			if decodeErr != nil {
				return CodeDiffResult{}, fmt.Errorf("invalid code diff cursor")
			}
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
			Base:         diffBase8,
			Paths:        paths,
			Diff:         strings.Join(candidateLines, ""),
			Pagination:   codePagination(pagination.EncodeServerCursor(kind, strconv.FormatInt(lineOffset+1, 10))),
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
			pageCursor = pagination.EncodeServerCursor(kind, strconv.FormatInt(nextOffset, 10))
		}
		return CodeDiffResult{
			CodeIdentity: target.CodeIdentity,
			Base:         diffBase8,
			Paths:        paths,
			Diff:         strings.Join(pageLines, ""),
			Pagination:   codePagination(pageCursor),
		}, nil
	}
	if !continuation {
		result := CodeDiffResult{
			CodeIdentity: target.CodeIdentity,
			Base:         diffBase8,
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
