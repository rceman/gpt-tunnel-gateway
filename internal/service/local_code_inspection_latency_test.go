package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCodeSearchCleanMainLatencyAndResolutionWork(t *testing.T) {
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
			samples := make([]float64, 0, 5)
			var cursor string
			for repeat := 0; repeat < 5; repeat++ {
				input := testCase.input
				input.ProjectID = "example"
				if testCase.name == "paginated-context" && repeat > 0 {
					input.Cursor = cursor
				}
				started := time.Now()
				result, err := f.service.CodeSearch(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				samples = append(samples, float64(time.Since(started).Microseconds())/1000)
				if testCase.name == "paginated-context" && repeat == 0 {
					if result.Pagination == nil {
						t.Fatal("pagination case did not produce a continuation")
					}
					cursor = result.Pagination.NextCursor
				}
			}
			sort.Float64s(samples)
			t.Logf("case=%s samples_ms=%v median_ms=%.3f p95ish_ms=%.3f", testCase.name, samples, samples[2], samples[len(samples)-1])
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
