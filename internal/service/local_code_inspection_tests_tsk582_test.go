package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func installTSK582ScanBudgetDouble(t *testing.T, f localCodeFixture, total int, matching map[int]bool) {
	t.Helper()
	target := localCodeTarget{
		CodeIdentity: CodeIdentity{
			ProjectID:   "example",
			Worktree:    "WT-MAIN-dddddddd",
			CurrentHead: strings.Repeat("d", 40),
			Live:        true,
		},
		ProjectWorktree: f.service.Config.Projects["example"],
		Kind:            "main",
	}
	f.service.codeTargetResolver = func(context.Context, string, string, bool) (localCodeTarget, error) {
		return target, nil
	}
	f.service.codePathWalker = func(_ context.Context, _ localCodeTarget, _ []string, _ string, _ []string, _ []string, visit func(string) error) error {
		for index := 0; index < total; index++ {
			if err := visit(fmt.Sprintf("scan-%05d.txt", index)); err != nil {
				return err
			}
		}
		return nil
	}
	f.service.codeFileReader = func(_ context.Context, _ localCodeTarget, pathName string) (string, error) {
		index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(pathName, "scan-"), ".txt"))
		if err != nil {
			return "", err
		}
		if matching[index] {
			return "needle\n", nil
		}
		return "no match\n", nil
	}
}

func TestTSK582CodeSearchScanBudgetReturnsPartialPageAndContinuation(t *testing.T) {
	f := newLocalCodeFixture(t)
	total := LocalCodeScanLookahead + 2
	installTSK582ScanBudgetDouble(t, f, total, map[int]bool{0: true, LocalCodeScanLookahead + 1: true})
	ctx := context.Background()
	first, err := f.service.CodeSearch(ctx, CodeSearchInput{
		ProjectID: "example",
		Worktree:  "WT-MAIN-dddddddd",
		Query:     "needle",
		Live:      true,
	})
	if err != nil || len(first.Matches) != 1 || first.Matches[0].Path != "scan-00000.txt" || first.PathsScanned != LocalCodeScanLookahead || first.Pagination == nil || first.Pagination.NextCursor == "" {
		t.Fatalf("scan-budget search first page=%#v err=%v", first, err)
	}
	second, err := f.service.CodeSearch(ctx, CodeSearchInput{
		ProjectID: "example",
		Worktree:  "WT-MAIN-dddddddd",
		Query:     "needle",
		Cursor:    first.Pagination.NextCursor,
		Live:      true,
	})
	if err != nil || len(second.Matches) != 1 || second.Matches[0].Path != fmt.Sprintf("scan-%05d.txt", LocalCodeScanLookahead+1) || second.Pagination != nil {
		t.Fatalf("scan-budget search continuation=%#v err=%v", second, err)
	}
}

func TestTSK582CodeTreeScanBudgetReturnsDeterministicContinuation(t *testing.T) {
	f := newLocalCodeFixture(t)
	total := LocalCodeScanLookahead + 2
	installTSK582ScanBudgetDouble(t, f, total, nil)
	ctx := context.Background()
	first, err := f.service.CodeTree(ctx, CodeTreeInput{
		ProjectID: "example",
		Worktree:  "WT-MAIN-dddddddd",
		Live:      true,
	})
	if err != nil || len(first.Paths) == 0 || first.Pagination == nil || first.Pagination.NextCursor == "" {
		t.Fatalf("scan-budget tree first page=%#v err=%v", first, err)
	}
	seen := make(map[string]struct{}, total)
	for _, pathName := range first.Paths {
		seen[pathName] = struct{}{}
	}
	cursor := first.Pagination.NextCursor
	pages := 1
	for cursor != "" {
		if pages > total {
			t.Fatal("scan-budget tree continuation did not terminate")
		}
		page, pageErr := f.service.CodeTree(ctx, CodeTreeInput{
			ProjectID: "example",
			Worktree:  "WT-MAIN-dddddddd",
			Cursor:    cursor,
			Live:      true,
		})
		if pageErr != nil {
			t.Fatalf("tree page %d error: %v", pages+1, pageErr)
		}
		for _, pathName := range page.Paths {
			if _, exists := seen[pathName]; exists {
				t.Fatalf("tree path repeated across pages: %q", pathName)
			}
			seen[pathName] = struct{}{}
		}
		pages++
		cursor = ""
		if page.Pagination != nil {
			cursor = page.Pagination.NextCursor
		}
	}
	if pages < 2 || len(seen) != total {
		t.Fatalf("tree pages=%d paths=%d want pages > 1 and paths=%d", pages, len(seen), total)
	}
}

func TestTSK582RetainsLocalCodeRequestBounds(t *testing.T) {
	paths := make([]string, LocalCodeMaxPaths+1)
	for index := range paths {
		paths[index] = fmt.Sprintf("path-%d", index)
	}
	if _, err := validateLocalCodePaths(paths, false); err == nil {
		t.Fatal("LocalCodeMaxPaths bound was removed")
	}
	patterns := make([]string, LocalCodeMaxPatterns+1)
	for index := range patterns {
		patterns[index] = fmt.Sprintf("path-%d", index)
	}
	if err := validateCodePatterns(patterns); err == nil {
		t.Fatal("LocalCodeMaxPatterns bound was removed")
	}
	alternatives := make([]string, LocalCodeMaxSearchAlternatives+1)
	for index := range alternatives {
		alternatives[index] = fmt.Sprintf("needle-%d", index)
	}
	if _, err := parseCodeSearchQuery(strings.Join(alternatives, "|"), false); err == nil {
		t.Fatal("LocalCodeMaxSearchAlternatives bound was removed")
	}

	f := newLocalCodeFixture(t)
	installLocalCodeBehaviorDouble(t, f, map[string]string{"large.txt": strings.Repeat("x", LocalCodeMaxBytes+1)})
	_, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example",
		Worktree:  "WT-MAIN-dddddddd",
		Query:     "x",
		Paths:     []string{"large.txt"},
		Live:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "bounded object limit") {
		t.Fatalf("LocalCodeMaxBytes bound was removed: %v", err)
	}
}
