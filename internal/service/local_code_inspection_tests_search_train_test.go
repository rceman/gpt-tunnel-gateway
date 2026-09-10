package service

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestLocalCodeSearchSkipsPreCursorFileContents(t *testing.T) {
	f := newLocalCodeFixture(t)
	for name, content := range map[string]string{
		"a-before.txt": "ordinary content\n",
		"b-match.txt":  "needle first\n",
		"c-match.txt":  "needle second\n",
	} {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	installLocalCodeBehaviorDouble(t, f, map[string]string{
		"a-before.txt": "ordinary content\n",
		"b-match.txt":  "needle first\n",
		"c-match.txt":  "needle second\n",
	})
	t.Cleanup(func() {
		for _, name := range []string{"a-before.txt", "b-match.txt", "c-match.txt"} {
			_ = os.Remove(filepath.Join(f.root, name))
		}
	})
	reads := make(map[string]int)
	f.service.codeFileReader = func(ctx context.Context, target localCodeTarget, pathName string) (string, error) {
		reads[pathName]++
		return f.service.Git.ReadWorkingFile(ctx, target.ProjectWorktree, pathName)
	}
	selector := "WT-MAIN-" + f.current[:8]
	paths := []string{"a-before.txt", "b-match.txt", "c-match.txt"}
	target, err := f.service.resolveLocalCodeTarget(context.Background(), "example", selector, true)
	if err != nil {
		t.Fatal(err)
	}
	kind := codeCursorKind("code-search", target, "needle|"+strings.Join(paths, "\x00")+"|||0|true")
	cursor := pagination.EncodeSearchCursor(kind, "a-before.txt", 0)
	result, err := f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example",
		Worktree:  selector,
		Live:      true,
		Query:     "needle",
		Paths:     paths,
		Cursor:    cursor,
	})
	if err != nil || len(result.Matches) != 2 || result.Pagination != nil {
		t.Fatalf("pre-cursor search result was not complete: %#v %v", result, err)
	}
	if reads["a-before.txt"] != 0 || reads["b-match.txt"] != 1 || reads["c-match.txt"] != 1 {
		t.Fatalf("pre-cursor file contents were reread: %#v", reads)
	}
}

func TestLocalCodeScanSafetyFailsClosedWithoutPagination(t *testing.T) {
	f := newLocalCodeFixture(t)
	for index := 0; index < LocalCodeScanLookahead; index++ {
		name := filepath.Join(f.root, "scan", strconv.Itoa(index)+".txt")
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("ordinary content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, f.root, "add", "scan")
	testutil.Git(t, f.root, "commit", "-m", "scan bound fixture")
	// RefreshDefaultBranch is the production authority for canonical main;
	// publish the fixture commit before asking the service for its selector.
	testutil.Git(t, f.root, "push", "origin", "HEAD:refs/heads/main")
	worktrees, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	selector := ""
	for _, item := range worktrees.Items {
		if item.Kind == "main" {
			selector = item.Selector
			break
		}
	}
	if selector == "" {
		t.Fatalf("CodeWorktree returned no canonical main selector: %#v", worktrees.Items)
	}
	tree, treeErr := f.service.CodeTree(context.Background(), CodeTreeInput{
		ProjectID: "example",
		Worktree:  selector,
		Query:     "absent-tree",
	})
	if treeErr == nil || !strings.Contains(treeErr.Error(), "scan exceeded bounded work") {
		t.Fatalf("zero-match tree scan did not fail closed: %#v %v", tree, treeErr)
	}
	_, err = f.service.CodeSearch(context.Background(), CodeSearchInput{
		ProjectID: "example",
		Worktree:  selector,
		Query:     "absent-query",
	})
	if err == nil || !strings.Contains(err.Error(), "scan exceeded bounded work") {
		t.Fatalf("rare-match scan did not fail closed: %v", err)
	}
}

func TestCodeTrainWorktreePathUsesProjectCodeForCompactLane(t *testing.T) {
	stateDir := t.TempDir()
	project := config.ProjectConfig{ProjectCode: "GTW"}
	want := filepath.Join(stateDir, "work", "GTW", "TRN63")
	path, err := codeTrainWorktreePath(stateDir, "gpt-tunnel-gateway", project, "GTW-TRN63", nil)
	if err != nil || path != want {
		t.Fatalf("unexpected compact Train path: %q %v", path, err)
	}
	runtime := &trainv2.RuntimeBinding{ProjectID: "gpt-tunnel-gateway", ProjectCode: "GTW", TrainID: "GTW-TRN63", WorktreePath: want}
	if path, err := codeTrainWorktreePath(stateDir, "gpt-tunnel-gateway", project, "GTW-TRN63", runtime); err != nil || path != want {
		t.Fatalf("runtime compact Train path was rejected: %q %v", path, err)
	}
}
