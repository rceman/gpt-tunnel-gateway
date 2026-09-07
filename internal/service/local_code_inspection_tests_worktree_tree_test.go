package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestLocalCodeReadFileHashIsWholeFileAndStableAcrossPages(t *testing.T) {
	f := newLocalCodeFixture(t)
	var content strings.Builder
	for line := 0; line < 100; line++ {
		content.WriteString(strings.Repeat("hash-token ", 40))
		content.WriteByte('\n')
	}
	pathName := filepath.Join(f.root, "hash.txt")
	if err := os.WriteFile(pathName, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pathName) })
	digest := sha256.Sum256([]byte(content.String()))
	wantHash := hex.EncodeToString(digest[:])[:8]
	selector := "WT-MAIN-" + f.current[:8]
	first, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "hash.txt", Live: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "hash.txt", Live: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.FileHash != wantHash || repeat.FileHash != wantHash || first.FileHash != repeat.FileHash {
		t.Fatalf("file hash was not stable whole-file SHA256/8: first=%q repeat=%q want=%q", first.FileHash, repeat.FileHash, wantHash)
	}
	if first.CurrentHead != f.current[:8] || repeat.CurrentHead != f.current[:8] || first.Content != repeat.Content || first.StartLine != repeat.StartLine || first.EndLine != repeat.EndLine {
		t.Fatalf("repeated page was not stable: first=%#v repeat=%#v", first, repeat)
	}
	if first.Pagination == nil {
		t.Fatal("large read did not produce a continuation page")
	}
	continuation, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "hash.txt", Cursor: first.Pagination.NextCursor, Live: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if continuation.FileHash != wantHash || continuation.CurrentHead != f.current[:8] {
		t.Fatalf("continuation changed file identity: %#v", continuation)
	}
}

func TestLocalCodeReadCommittedAndLiveHashesDifferWithSameHead(t *testing.T) {
	f := newLocalCodeFixture(t)
	selector := "WT-MAIN-" + f.current[:8]
	committed, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "tracked.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	committedBytes := []byte("candidate tracked content with needle\n")
	committedDigest := sha256.Sum256(committedBytes)
	wantCommittedHash := hex.EncodeToString(committedDigest[:])[:8]
	if committed.FileHash != wantCommittedHash || committed.CurrentHead != f.current[:8] {
		t.Fatalf("committed read identity mismatch: got hash=%q head=%q want hash=%q head=%q", committed.FileHash, committed.CurrentHead, wantCommittedHash, f.current[:8])
	}

	liveBytes := []byte("dirty live content with needle\n")
	if err := os.WriteFile(filepath.Join(f.root, "tracked.txt"), liveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(f.root, "tracked.txt"), committedBytes, 0o600) })
	live, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example", Worktree: selector, Path: "tracked.txt", Live: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	liveDigest := sha256.Sum256(liveBytes)
	wantLiveHash := hex.EncodeToString(liveDigest[:])[:8]
	if live.FileHash != wantLiveHash || live.CurrentHead != f.current[:8] {
		t.Fatalf("live read identity mismatch: got hash=%q head=%q want hash=%q head=%q", live.FileHash, live.CurrentHead, wantLiveHash, f.current[:8])
	}
	if committed.FileHash == live.FileHash {
		t.Fatalf("committed and live hashes unexpectedly match: %q", committed.FileHash)
	}
}

func TestLocalCodeInspectionRequiresSharedDurabilityForWorktreeDiscovery(t *testing.T) {
	f := newLocalCodeFixture(t)
	f.service.Durability = nil
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), "Shared durability unavailable") {
		t.Fatalf("missing Shared durability was not fail-closed: %v", err)
	}
}

func TestCodeWorktreeUsesCurrentMainWorktreeFromInventory(t *testing.T) {
	f := newLocalCodeFixture(t)
	staleRoot := filepath.Join(t.TempDir(), "stale-main")
	testutil.Git(t, f.root, "worktree", "add", "--detach", staleRoot, f.base)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", staleRoot)
	})

	project := f.service.Config.Projects["example"]
	project.Root = staleRoot
	f.service.Config.Projects["example"] = project

	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatalf("CodeWorktree() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("CodeWorktree() items = %d, want 1", len(result.Items))
	}
	if result.Items[0].Head != f.current {
		t.Fatalf("main head = %q, want current %q", result.Items[0].Head, f.current)
	}
	wantSelector := "WT-MAIN-" + f.current[:8]
	if result.Items[0].Selector != wantSelector {
		t.Fatalf("main selector = %q, want %q", result.Items[0].Selector, wantSelector)
	}
}

func TestCodeWorktreeRefreshesCanonicalMainWhenConfiguredWorktreeIsStale(t *testing.T) {
	f := newLocalCodeFixture(t)
	remoteWorktree := filepath.Join(t.TempDir(), "remote-main")
	testutil.Git(t, f.root, "worktree", "add", "--detach", remoteWorktree, f.current)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", remoteWorktree)
	})
	if err := os.WriteFile(filepath.Join(remoteWorktree, "canonical-main.txt"), []byte("canonical main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, remoteWorktree, "add", "canonical-main.txt")
	testutil.Git(t, remoteWorktree, "commit", "-m", "advance canonical main")
	canonical := strings.TrimSpace(testutil.Git(t, remoteWorktree, "rev-parse", "HEAD"))
	testutil.Git(t, remoteWorktree, "push", "origin", "HEAD:refs/heads/main")

	before := strings.TrimSpace(testutil.Git(t, f.root, "rev-parse", "HEAD"))
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("read-only CodeWorktree did not fail closed for stale main: %v", err)
	}
	if after := strings.TrimSpace(testutil.Git(t, f.root, "rev-parse", "HEAD")); after != before {
		t.Fatalf("read-only CodeWorktree mutated physical HEAD from %s to %s", before, after)
	}
	project := f.service.Config.Projects["example"]
	if _, err := f.service.synchronizeDefaultBranchWorktree(context.Background(), project, canonical); err != nil {
		t.Fatalf("authorized default-branch synchronization failed: %v", err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
		if err != nil || len(result.Items) != 1 || result.Items[0].Head != canonical || result.Items[0].Selector != "WT-MAIN-"+canonical[:8] {
			t.Fatalf("CodeWorktree() repeat=%d result=%#v err=%v", repeat, result, err)
		}
	}
	selector := "WT-MAIN-" + canonical[:8]
	read, err := f.service.CodeRead(context.Background(), CodeReadInput{ProjectID: "example", Worktree: selector, Path: "canonical-main.txt"})
	if err != nil || read.Content != "canonical main\n" || read.CurrentHead != canonical[:8] {
		t.Fatalf("CodeRead()=%#v err=%v", read, err)
	}
	tree, err := f.service.CodeTree(context.Background(), CodeTreeInput{ProjectID: "example", Worktree: selector, Path: "canonical-main.txt"})
	if err != nil || len(tree.Paths) != 1 || tree.Paths[0] != "canonical-main.txt" {
		t.Fatalf("CodeTree()=%#v err=%v", tree, err)
	}
	search, err := f.service.CodeSearch(context.Background(), CodeSearchInput{ProjectID: "example", Worktree: selector, Query: "canonical", Paths: []string{"canonical-main.txt"}})
	if err != nil || len(search.Matches) != 1 {
		t.Fatalf("CodeSearch()=%#v err=%v", search, err)
	}
	diff, err := f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: selector})
	if err != nil || diff.Diff != "" {
		t.Fatalf("CodeDiff()=%#v err=%v", diff, err)
	}
}

func TestCodeWorktreeUsesDistinctHotfixSelectorWhenHeadMatchesMain(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	slug := "same-head"
	branch := "hotfix/" + slug
	lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
	if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", branch, f.base)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	if err := os.WriteFile(filepath.Join(lane, "same-head.txt"), []byte("unmerged hotfix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, lane, "add", "same-head.txt")
	testutil.Git(t, lane, "commit", "-m", "unmerged same-head fixture")
	hotfixHead := strings.TrimSpace(testutil.Git(t, lane, "rev-parse", "HEAD"))
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.base, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatalf("CodeWorktree() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("CodeWorktree() items = %d, want 2: %#v", len(result.Items), result.Items)
	}
	if result.Items[0].Selector != "WT-MAIN-"+f.current[:8] {
		t.Fatalf("main selector = %q", result.Items[0].Selector)
	}
	wantHotfix := "WT-FIX-" + slug + "-" + hotfixHead[:8]
	if result.Items[1].Selector != wantHotfix {
		t.Fatalf("hotfix selector = %q, want %q", result.Items[1].Selector, wantHotfix)
	}
}
