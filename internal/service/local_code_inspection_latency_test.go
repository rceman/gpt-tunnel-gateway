package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCodeSearchCleanMainResolutionFunctional(t *testing.T) {
	f := newLocalCodeFixture(t)
	if err := os.MkdirAll(filepath.Join(f.root, "internal"), 0o700); err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	for line := 0; line < 512; line++ {
		content.WriteString("latency needle line\n")
	}
	if err := os.WriteFile(filepath.Join(f.root, "internal", "search.txt"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "add", "internal/search.txt")
	testutil.Git(t, f.root, "commit", "-m", "latency fixture")
	f.current = strings.TrimSpace(testutil.Git(t, f.root, "rev-parse", "HEAD"))
	testutil.Git(t, f.root, "push", "origin", "main")

	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "git.log")
	gitWrapper := filepath.Join(binDir, "git")
	const wrapper = "#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec /usr/bin/git \"$@\"\n"
	if err := os.WriteFile(gitWrapper, []byte(fmt.Sprintf(wrapper, logPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	selector := "WT-MAIN-" + f.current[:8]
	cases := []struct {
		name  string
		input CodeSearchInput
	}{
		{name: "known-file", input: CodeSearchInput{
			Worktree: selector,
			Query:    "needle",
			Paths:    []string{"tracked.txt"},
		}},
		{name: "small-include", input: CodeSearchInput{
			Worktree: selector,
			Query:    "needle",
			Include:  []string{"*.txt"},
		}},
		{name: "broad-internal", input: CodeSearchInput{
			Worktree: selector,
			Query:    "needle",
			Include:  []string{"internal/*"},
		}},
		{name: "zero-match", input: CodeSearchInput{
			Worktree: selector,
			Query:    "absent",
		}},
		{name: "paginated-context", input: CodeSearchInput{
			Worktree:     selector,
			Query:        "needle",
			Paths:        []string{"internal/search.txt"},
			ContextLines: 1,
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := testCase.input
			input.ProjectID = "example"
			result, err := f.service.CodeSearch(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.name == "zero-match" && len(result.Matches) != 0 {
				t.Fatalf("zero-match search returned matches: %#v", result.Matches)
			}
			if testCase.name == "paginated-context" {
				if result.Pagination == nil || result.Pagination.NextCursor == "" {
					t.Fatal("pagination case did not produce a continuation")
				}
				continuation, continuationErr := f.service.CodeSearch(context.Background(), CodeSearchInput{
					ProjectID:    "example",
					Worktree:     selector,
					Query:        "needle",
					Paths:        []string{"internal/search.txt"},
					ContextLines: 1,
					Cursor:       result.Pagination.NextCursor,
				})
				if continuationErr != nil || len(continuation.Matches) == 0 {
					t.Fatalf("pagination continuation=%#v err=%v", continuation, continuationErr)
				}
			}
		})
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(log), "\n") {
		if strings.HasPrefix(line, "fetch ") || strings.HasPrefix(line, "worktree list") {
			t.Fatalf("clean-main search performed avoidable Git operation: %q", line)
		}
	}
}
