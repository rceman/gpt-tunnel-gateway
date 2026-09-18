package service

import (
	"context"
	"strings"
	"testing"
)

func tsk644SearchFixture(t *testing.T) (localCodeFixture, string) {
	t.Helper()
	f := newLocalCodeFixture(t)
	installLocalCodeBehaviorDouble(t, f, map[string]string{
		"alpha.txt": "alpha-only-marker\n",
		"beta.txt":  "beta-only-marker\n",
		"both.txt":  "alpha-only-marker and beta-only-marker\n",
		"pipe.txt":  "left | right\npath\\to\\file\n",
		"case.txt":  "MixedCaseMarker\n",
	})
	return f, "WT-MAIN-" + f.current[:8]
}

func tsk644Search(t *testing.T, f localCodeFixture, selector string, in CodeSearchInput) CodeSearchResult {
	t.Helper()
	in.ProjectID = "example"
	in.Worktree = selector
	result, err := f.service.CodeSearch(context.Background(), in)
	if err != nil {
		t.Fatalf("CodeSearch(%+v) failed: %v", in, err)
	}
	return result
}

func tsk644SearchPaths(result CodeSearchResult) []string {
	paths := make([]string, 0, len(result.Matches))
	for _, match := range result.Matches {
		paths = append(paths, match.Path)
	}
	return paths
}

func TestTSK644CodeSearchLiteralAlternativesMatchInOnePass(t *testing.T) {
	f, selector := tsk644SearchFixture(t)
	reads := 0
	reader := f.service.codeFileReader
	f.service.codeFileReader = func(ctx context.Context, target localCodeTarget, pathName string) (string, error) {
		reads++
		return reader(ctx, target, pathName)
	}
	paths := []string{"alpha.txt", "beta.txt", "both.txt"}

	single := tsk644Search(t, f, selector, CodeSearchInput{
		Query: "alpha-only-marker",
		Paths: paths,
	})
	if got := tsk644SearchPaths(single); len(got) != 2 || got[0] != "alpha.txt" || got[1] != "both.txt" {
		t.Fatalf("single literal matches=%v", got)
	}
	if reads != len(paths) {
		t.Fatalf("single literal read %d files for %d paths", reads, len(paths))
	}

	reads = 0
	alternatives := tsk644Search(t, f, selector, CodeSearchInput{
		Query: "alpha-only-marker|beta-only-marker",
		Paths: paths,
	})
	if got := tsk644SearchPaths(alternatives); len(got) != 3 || got[0] != "alpha.txt" || got[1] != "beta.txt" || got[2] != "both.txt" {
		t.Fatalf("alternative matches=%v", got)
	}
	if reads != len(paths) {
		t.Fatalf("alternatives caused %d file reads over %d paths, want one bounded pass", reads, len(paths))
	}

	filtered := tsk644Search(t, f, selector, CodeSearchInput{
		Query: "alpha-only-marker|beta-only-marker",
		Paths: []string{"alpha.txt"},
	})
	if got := tsk644SearchPaths(filtered); len(got) != 1 || got[0] != "alpha.txt" {
		t.Fatalf("path-filtered alternatives=%v", got)
	}
	excluded := tsk644Search(t, f, selector, CodeSearchInput{
		Query:   "alpha-only-marker|beta-only-marker",
		Paths:   paths,
		Exclude: []string{"both*"},
	})
	if got := tsk644SearchPaths(excluded); len(got) != 2 || got[1] != "beta.txt" {
		t.Fatalf("exclude-filtered alternatives=%v", got)
	}
}

func TestTSK644CodeSearchEscapesAndCaseModes(t *testing.T) {
	f, selector := tsk644SearchFixture(t)

	pipe := tsk644Search(t, f, selector, CodeSearchInput{
		Query: `left \| right`,
		Paths: []string{"pipe.txt"},
	})
	if len(pipe.Matches) != 1 || pipe.Matches[0].Line != 1 {
		t.Fatalf("escaped pipe matches=%#v", pipe.Matches)
	}
	backslash := tsk644Search(t, f, selector, CodeSearchInput{
		Query: `path\\to\\file`,
		Paths: []string{"pipe.txt"},
	})
	if len(backslash.Matches) != 1 || backslash.Matches[0].Line != 2 {
		t.Fatalf("escaped backslash matches=%#v", backslash.Matches)
	}

	sensitive := tsk644Search(t, f, selector, CodeSearchInput{
		Query: "mixedcasemarker",
		Paths: []string{"case.txt"},
	})
	if len(sensitive.Matches) != 0 {
		t.Fatalf("default search was case-insensitive: %#v", sensitive.Matches)
	}
	insensitive := tsk644Search(t, f, selector, CodeSearchInput{
		Query:           "mixedcasemarker",
		Paths:           []string{"case.txt"},
		CaseInsensitive: true,
	})
	if len(insensitive.Matches) != 1 || insensitive.Matches[0].Snippet != "MixedCaseMarker" {
		t.Fatalf("case-insensitive matches=%#v", insensitive.Matches)
	}
	everyAlternative := tsk644Search(t, f, selector, CodeSearchInput{
		Query:           "alpha-only-marker|MIXEDCASEMARKER",
		Paths:           []string{"alpha.txt", "case.txt"},
		CaseInsensitive: true,
	})
	if got := tsk644SearchPaths(everyAlternative); len(got) != 2 {
		t.Fatalf("case_insensitive did not apply to every alternative: %v", got)
	}
}

func TestTSK644CodeSearchFailsClosedOnMalformedQueries(t *testing.T) {
	f, selector := tsk644SearchFixture(t)
	for name, query := range map[string]string{
		"empty query":        "",
		"empty alternative":  "alpha||beta",
		"leading pipe":       "|alpha",
		"trailing pipe":      "alpha|",
		"trailing escape":    `alpha\`,
		"unsupported escape": `alpha\beta`,
		"newline":            "alpha\nbeta",
	} {
		_, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
			ProjectID: "example",
			Worktree:  selector,
			Query:     query,
			Paths:     []string{"alpha.txt"},
		})
		if err == nil {
			t.Fatalf("%s query %q was accepted", name, query)
		}
	}
}

func TestTSK644CodeSearchAlternativesPaginateAndBindCaseMode(t *testing.T) {
	f := newLocalCodeFixture(t)
	var content strings.Builder
	for index := 0; index < 256; index++ {
		content.WriteString("needle tokenized search line\n")
	}
	installLocalCodeBehaviorDouble(t, f, map[string]string{"many-matches.txt": content.String()})
	selector := "WT-MAIN-" + f.current[:8]

	first := tsk644Search(t, f, selector, CodeSearchInput{
		Query: "needle|tokenized",
		Paths: []string{"many-matches.txt"},
	})
	if len(first.Matches) < 2 || first.Pagination == nil || first.Pagination.NextCursor == "" {
		t.Fatalf("expected a bounded first alternative page: %#v", first)
	}
	second := tsk644Search(t, f, selector, CodeSearchInput{
		Query:  "needle|tokenized",
		Paths:  []string{"many-matches.txt"},
		Cursor: first.Pagination.NextCursor,
	})
	if len(second.Matches) == 0 || second.Matches[0].Line <= first.Matches[len(first.Matches)-1].Line {
		t.Fatalf("alternative continuation did not advance: first=%#v second=%#v", first, second)
	}
	if _, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID:       "example",
		Worktree:        selector,
		Query:           "needle|tokenized",
		Paths:           []string{"many-matches.txt"},
		CaseInsensitive: true,
		Cursor:          first.Pagination.NextCursor,
	}); err == nil {
		t.Fatal("case-insensitive search accepted a case-sensitive continuation cursor")
	}
}
