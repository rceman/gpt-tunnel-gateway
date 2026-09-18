package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type tsk645DiffHeadCandidates struct {
	worktree  string
	baseHead  string
	codeHead  string
	testsHead string
}

func tsk645DiffHeadFixture(t *testing.T) (*Service, tsk645DiffHeadCandidates) {
	t.Helper()
	ctx := context.Background()
	s, db := tsk585Setup(t)
	t.Cleanup(func() { _ = db.Close() })
	task := tsk585Task(t, s, "tsk645-diff-head", "Diff head fixture")
	tsk585Dispatch(t, s, task.ID)
	lines := make([]string, 400)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %04d of a long generated diff body to exceed the page token budget limit", i)
	}
	tsk585LaneWrite(t, s, task.ID, "big.txt", strings.Join(lines, "\n"))
	tsk585LaneWrite(t, s, task.ID, "code.txt", "code candidate line\n")
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("read code state found=%v err=%v", found, err)
	}
	codeHead := state.Head
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
		Decision:  "accept",
	}); err != nil {
		t.Fatal(err)
	}
	tsk585LaneWrite(t, s, task.ID, "tests.txt", "tests candidate line\n")
	if _, err := s.TaskExecutionSubmitTests(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	state, found, err = db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("read tests state found=%v err=%v", found, err)
	}
	if state.Head == codeHead {
		t.Fatal("fixture must record two distinct candidates")
	}
	return s, tsk645DiffHeadCandidates{
		worktree:  state.Worktree,
		baseHead:  state.BaseHead,
		codeHead:  codeHead,
		testsHead: state.Head,
	}
}

func tsk645DiffAll(t *testing.T, s *Service, in CodeDiffInput) (CodeDiffResult, string) {
	t.Helper()
	ctx := context.Background()
	result, err := s.CodeDiff(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	all.WriteString(result.Diff)
	cursor := ""
	if result.Pagination != nil {
		cursor = result.Pagination.NextCursor
	}
	for cursor != "" {
		in.Cursor = cursor
		page, pageErr := s.CodeDiff(ctx, in)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		all.WriteString(page.Diff)
		cursor = ""
		if page.Pagination != nil {
			cursor = page.Pagination.NextCursor
		}
	}
	return result, all.String()
}

func TestTSK645CodeDiffAuthoritativeHead(t *testing.T) {
	ctx := context.Background()
	s, fixture := tsk645DiffHeadFixture(t)
	code8 := strings.ToLower(fixture.codeHead[:8])
	tests8 := strings.ToLower(fixture.testsHead[:8])

	unchanged, unchangedDiff := tsk645DiffAll(t, s, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      tests8,
	})
	if unchanged.Base != strings.ToLower(fixture.baseHead[:8]) {
		t.Fatalf("head diff base=%q want %q", unchanged.Base, fixture.baseHead[:8])
	}
	if unchanged.CurrentHead != fixture.testsHead || unchanged.CurrentHead[:8] != tests8 {
		t.Fatalf("unchanged candidate diff head=%q want %q", unchanged.CurrentHead, fixture.testsHead)
	}
	if !strings.Contains(unchangedDiff, "tests candidate line") || !strings.Contains(unchangedDiff, "code candidate line") {
		t.Fatalf("unchanged candidate diff missing candidate content: %q", unchangedDiff)
	}

	_, defaultDiff := tsk645DiffAll(t, s, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
	})
	if defaultDiff != unchangedDiff {
		t.Fatal("unchanged candidate head must reproduce the default diff exactly")
	}

	prior, priorDiff := tsk645DiffAll(t, s, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      code8,
	})
	if prior.CurrentHead != fixture.codeHead || prior.CurrentHead[:8] != code8 {
		t.Fatalf("prior candidate diff head=%q want %q", prior.CurrentHead, fixture.codeHead)
	}
	if prior.CurrentHead == fixture.testsHead {
		t.Fatal("prior candidate diff head must not report the worktree head")
	}
	if !strings.Contains(priorDiff, "code candidate line") || strings.Contains(priorDiff, "tests candidate line") {
		t.Fatalf("prior candidate diff must stop at the recorded code head: %q", priorDiff)
	}
	if priorDiff == unchangedDiff {
		t.Fatal("prior candidate diff must differ from the current candidate diff")
	}

	_, filtered := tsk645DiffAll(t, s, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      code8,
		Paths:     []string{"code.txt"},
	})
	if !strings.Contains(filtered, "code candidate line") || strings.Contains(filtered, "big.txt") {
		t.Fatalf("path-filtered head diff=%q", filtered)
	}
	absent, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      code8,
		Paths:     []string{"tests.txt"},
	})
	if err != nil || absent.Diff != "" {
		t.Fatalf("head diff for a path absent at the head must be empty: %#v err=%v", absent, err)
	}

	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      code8,
		Live:      true,
	}); err == nil {
		t.Fatal("live observation must reject an explicit head")
	}
	tsk585AdvanceCanonical(t, s)
	for name, head := range map[string]string{
		"unknown":      "00000000",
		"uppercase":    strings.ToUpper(code8),
		"non-sha8":     fixture.codeHead[:9],
		"canonical":    strings.ToLower(tsk585MainHead(t, s)[:8]),
		"foreign-head": strings.ToLower(fixture.testsHead[1:9]),
	} {
		if _, err := s.CodeDiff(ctx, CodeDiffInput{
			ProjectID: "example",
			Worktree:  fixture.worktree,
			Head:      head,
		}); err == nil {
			t.Fatalf("%s head %q must fail closed", name, head)
		}
	}

	mainSelector := "WT-MAIN-" + strings.ToLower(tsk585MainHead(t, s)[:8])
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  mainSelector,
		Head:      code8,
	}); err == nil {
		t.Fatal("head on a non-Task selector must fail")
	}
}

func TestTSK645CodeDiffHeadBoundedPagination(t *testing.T) {
	ctx := context.Background()
	s, fixture := tsk645DiffHeadFixture(t)
	code8 := strings.ToLower(fixture.codeHead[:8])
	tests8 := strings.ToLower(fixture.testsHead[:8])

	first, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      code8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Pagination == nil || first.Pagination.NextCursor == "" {
		t.Fatalf("large head diff must paginate: %#v", first)
	}
	next, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      code8,
		Cursor:    first.Pagination.NextCursor,
	})
	if err != nil {
		t.Fatalf("head diff continuation: %v", err)
	}
	if next.Diff == "" {
		t.Fatal("head diff continuation returned no lines")
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Head:      tests8,
		Cursor:    first.Pagination.NextCursor,
	}); err == nil {
		t.Fatal("head diff cursor must not cross to another head")
	}
	if _, err := s.CodeDiff(ctx, CodeDiffInput{
		ProjectID: "example",
		Worktree:  fixture.worktree,
		Cursor:    first.Pagination.NextCursor,
	}); err == nil {
		t.Fatal("head diff cursor must not cross to the omitted head")
	}
}
