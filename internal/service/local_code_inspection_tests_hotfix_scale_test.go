package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCodeReadResolvesHotfixDirectlyBeyondIdentityInventoryLimit(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	slug := "direct-target"
	branch := "hotfix/" + slug
	lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
	testutil.Git(t, f.root, "branch", branch, f.base)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.base, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
			ProjectID: "example", HotfixRef: fmt.Sprintf("refs/heads/hotfix/decoy-%03d", i), TaskID: fmt.Sprintf("EXM-TSK%d", i+2), BaseSHA: f.base, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	selector := "WT-FIX-" + slug + "-" + f.base[:8]
	read, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "tracked.txt",
	})
	if err != nil {
		t.Fatalf("CodeRead() with >100 managed identities: %v", err)
	}
	if read.CurrentHead != f.base[:8] || read.Content != "base tracked content\n" {
		t.Fatalf("CodeRead()=%#v, want target hotfix content", read)
	}
}

func TestTSK579CodeWorktreePagesLargeManagedInventoryAndExactSelector(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	for i := 0; i < 101; i++ {
		slug := fmt.Sprintf("page-%03d-%s", i, strings.Repeat("x", 40))
		branch := "hotfix/" + slug
		lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
		testutil.Git(t, f.root, "branch", branch, f.base)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
			ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: fmt.Sprintf("EXM-TSK%d", i+1), BaseSHA: f.base, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
	}

	cursor := ""
	seen := make(map[string]struct{}, 102)
	firstPage := true
	for pageNumber := 0; ; pageNumber++ {
		result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{
			ProjectID: "example",
			Cursor:    cursor,
		})
		if err != nil {
			t.Fatal(err)
		}
		if firstPage && (len(result.Items) >= 102 || result.Pagination == nil || result.Pagination.NextCursor == "") {
			t.Fatalf("first page was not bounded: items=%d pagination=%#v", len(result.Items), result.Pagination)
		}
		firstPage = false
		for _, item := range result.Items {
			if _, exists := seen[item.Selector]; exists {
				t.Fatalf("duplicate worktree %q", item.Selector)
			}
			seen[item.Selector] = struct{}{}
		}
		if result.Pagination == nil || result.Pagination.NextCursor == "" {
			break
		}
		if pageNumber >= 102 {
			t.Fatal("public CodeWorktree pagination did not terminate")
		}
		cursor = result.Pagination.NextCursor
	}
	if len(seen) != 102 {
		t.Fatalf("public worktree identities=%d, want main plus 101 hotfixes", len(seen))
	}
	selector := "WT-FIX-page-050-" + strings.Repeat("x", 40) + "-" + f.base[:8]
	read, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "tracked.txt",
	})
	if err != nil {
		t.Fatalf("exact selector with large inventory: %v", err)
	}
	if read.CurrentHead != f.base[:8] || read.Content != "base tracked content\n" {
		t.Fatalf("exact selector read=%#v", read)
	}
}
