package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalCodeReadSupportsExactBoundedRangesAndContinuation(t *testing.T) {
	f := newLocalCodeFixture(t)
	selector := "WT-MAIN-" + f.current[:8]
	immutableCount := 1
	immutable, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "tracked.txt",
		StartLine: 1,
		LineCount: &immutableCount,
	})
	if err != nil || immutable.CurrentHead != f.current[:8] || immutable.StartLine != 1 || immutable.EndLine != 1 || immutable.Content != "candidate tracked content with needle" || immutable.Pagination != nil {
		t.Fatalf("live=false bounded read was not an immutable exact range: %#v %v", immutable, err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(filepath.Join(f.root, name)) })
	}
	write("range.txt", strings.Join([]string{
		"line-01", "line-02", "line-03", "line-04", "line-05", "line-06", "line-07", "line-08",
	}, "\n"))
	var wide strings.Builder
	for line := 1; line <= 120; line++ {
		fmt.Fprintf(&wide, "%03d %s\n", line, strings.Repeat("range-token ", 48))
	}
	write("wide.txt", wide.String())
	shortCount := 2
	short, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "range.txt",
		StartLine: 3,
		LineCount: &shortCount,
		Live:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if short.CurrentHead != f.current[:8] || short.StartLine != 3 || short.EndLine != 4 || short.TotalLines != 8 || short.Content != "line-03\nline-04" || short.Pagination != nil {
		t.Fatalf("unexpected exact short range: %#v", short)
	}

	nearEOFCount := 5
	nearEOF, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "range.txt",
		StartLine: 7,
		LineCount: &nearEOFCount,
		Live:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nearEOF.StartLine != 7 || nearEOF.EndLine != 8 || nearEOF.Content != "line-07\nline-08" || nearEOF.Pagination != nil {
		t.Fatalf("near-EOF range was not clamped: %#v", nearEOF)
	}

	fileReads := 0
	f.service.codeFileReader = func(context.Context, localCodeTarget, string) (string, error) {
		fileReads++
		return "", nil
	}
	for _, invalidCount := range []int{0, -1} {
		_, err := f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example",
			Worktree:  selector,
			Path:      "range.txt",
			LineCount: &invalidCount,
			Live:      true,
		})
		if err == nil || !strings.Contains(err.Error(), "line_count") {
			t.Fatalf("invalid line_count %d was accepted: %v", invalidCount, err)
		}
	}
	if fileReads != 0 {
		t.Fatalf("invalid line_count triggered %d file reads", fileReads)
	}
	f.service.codeFileReader = nil
	outOfRangeCount := 1
	if _, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "range.txt",
		StartLine: 9,
		LineCount: &outOfRangeCount,
		Live:      true,
	}); err == nil || !strings.Contains(err.Error(), "start_line exceeds file") {
		t.Fatalf("out-of-range start_line was accepted: %v", err)
	}

	requestedCount := 100
	page, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "wide.txt",
		StartLine: 10,
		LineCount: &requestedCount,
		Live:      true,
	})
	if err != nil || page.Pagination == nil {
		t.Fatalf("oversized range did not continue: %#v %v", page, err)
	}
	if _, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "range.txt",
		Cursor:    page.Pagination.NextCursor,
		LineCount: &requestedCount,
		Live:      true,
	}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("cursor plus line_count was not rejected: %v", err)
	}
	wantStart, pages := 10, 0
	for {
		if page.CurrentHead != f.current[:8] || page.StartLine != wantStart || page.EndLine < page.StartLine || page.EndLine > 109 || !strings.HasPrefix(page.Content, fmt.Sprintf("%03d ", wantStart)) {
			t.Fatalf("range continuation was not exact: %#v", page)
		}
		pages++
		if page.Pagination == nil {
			if page.EndLine != 109 {
				t.Fatalf("range continuation ended at %d, want 109", page.EndLine)
			}
			break
		}
		if page.Pagination.NextCursor == "" {
			t.Fatal("range continuation omitted cursor")
		}
		conflictingCount := 1
		if _, err := f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example",
			Worktree:  selector,
			Path:      "wide.txt",
			Cursor:    page.Pagination.NextCursor,
			LineCount: &conflictingCount,
			Live:      true,
		}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("cursor plus line_count was not rejected: %v", err)
		}
		if pages > 100 {
			t.Fatal("range continuation did not terminate")
		}
		wantStart = page.EndLine + 1
		page, err = f.service.CodeRead(context.Background(), CodeReadInput{
			ProjectID: "example",
			Worktree:  selector,
			Path:      "wide.txt",
			Cursor:    page.Pagination.NextCursor,
			Live:      true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
