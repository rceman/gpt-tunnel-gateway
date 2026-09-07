package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestSortCodeWorktreeCandidatesUsesKindThenNewestCreationAndCanonicalIDDescending(t *testing.T) {
	now := time.Now().UTC()
	candidates := []codeWorktreeCandidate{
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "train-old",
			},
			Kind: "train",
		}, CreatedAt: now.Add(-time.Hour), SortID: "GTW-TRN2"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "hotfix-tie-b",
			},
			Kind: "hotfix",
		}, CreatedAt: now, SortID: "b-fix"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "main",
			},
			Kind: "main",
		}, SortID: "main"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "train-new",
			},
			Kind: "train",
		}, CreatedAt: now, SortID: "GTW-TRN3"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "hotfix-new",
			},
			Kind: "hotfix",
		}, CreatedAt: now.Add(time.Minute), SortID: "new-fix"},
		{localCodeTarget: localCodeTarget{
			CodeIdentity: CodeIdentity{
				Worktree: "hotfix-tie-a",
			},
			Kind: "hotfix",
		}, CreatedAt: now, SortID: "a-fix"},
	}
	sortCodeWorktreeCandidates(candidates)
	want := []string{"main", "hotfix-new", "hotfix-tie-b", "hotfix-tie-a", "train-new", "train-old"}
	got := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		got = append(got, candidate.CodeIdentity.Worktree)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidate order=%v want=%v", got, want)
	}
}

func TestCodeWorktreePagePacksByTokensAndPreservesOrder(t *testing.T) {
	items := make([]CodeWorktreeItem, 40)
	for index := range items {
		items[index] = CodeWorktreeItem{
			Selector: fmt.Sprintf("WT-MAIN-%08x", index+1),
			Kind:     "main",
			Label:    strings.Repeat(fmt.Sprintf("item-%02d ", index), 40),
		}
	}
	kind := "code-worktree|example|"
	cursor := ""
	got := make([]string, 0, len(items))
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber > len(items) {
			t.Fatal("worktree pagination did not terminate")
		}
		page, nextCursor, err := codeWorktreePage(kind, items, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page {
			got = append(got, item.Selector)
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	if len(got) != len(items) {
		t.Fatalf("paged %d items, want %d", len(got), len(items))
	}
	for index, item := range items {
		if got[index] != item.Selector {
			t.Fatalf("item %d=%q want %q", index, got[index], item.Selector)
		}
	}
}

func TestCodeWorktreeOrdersMainThenUnmergedHotfixesAndSkipsLegacy(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	now := time.Now().UTC()
	addHotfix := func(slug, content string, createdAt time.Time) {
		t.Helper()
		lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", slug)
		if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
			t.Fatal(err)
		}
		branch := "hotfix/" + slug
		testutil.Git(t, f.root, "branch", branch, f.current)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		if err := os.WriteFile(filepath.Join(lane, slug+".txt"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, lane, "add", slug+".txt")
		testutil.Git(t, lane, "commit", "-m", slug+" fixture")
		if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: createdAt}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
	}
	addHotfix("new", "new\n", now.Add(time.Minute))
	addHotfix("old", "old\n", now)
	legacyLane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", "legacy")
	testutil.Git(t, f.root, "branch", "hotfix/legacy", f.current)
	testutil.Git(t, f.root, "worktree", "add", legacyLane, "hotfix/legacy")
	legacyIdentity := filepath.Join(f.service.Config.StateDir, "hotfix-identities", "example", "legacy.json")
	legacyPayload := fmt.Sprintf(`{"project_id":"example","hotfix_ref":"refs/heads/hotfix/legacy","base_sha":%q,"created_at":"%s"}`, f.current, now.Add(2*time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(legacyIdentity, []byte(legacyPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", legacyLane)
		testutil.Git(t, f.root, "branch", "-D", "hotfix/legacy")
	})

	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 || result.Items[0].Kind != "main" || result.Items[1].Label != "new" || result.Items[2].Label != "old" {
		t.Fatalf("unexpected ordered worktrees: %#v", result.Items)
	}
	if strings.Contains(fmt.Sprint(result.Items), "legacy") {
		t.Fatalf("legacy hotfix was exposed: %#v", result.Items)
	}
}

func TestCodeWorktreeIgnoresStaleTrainAndOrphanHotfixRegistrations(t *testing.T) {
	f := newLocalCodeFixture(t)
	staleTrain := filepath.Join(t.TempDir(), "GTW-TRN7")
	testutil.Git(t, f.root, "branch", "train/GTW-TRN7", f.current)
	testutil.Git(t, f.root, "worktree", "add", staleTrain, "train/GTW-TRN7")
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", staleTrain)
		testutil.Git(t, f.root, "branch", "-D", "train/GTW-TRN7")
	})
	orphanHotfix := filepath.Join(t.TempDir(), "orphan-hotfix")
	testutil.Git(t, f.root, "branch", "hotfix/orphan", f.current)
	testutil.Git(t, f.root, "worktree", "add", orphanHotfix, "hotfix/orphan")
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", orphanHotfix)
		testutil.Git(t, f.root, "branch", "-D", "hotfix/orphan")
	})

	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Kind != "main" {
		t.Fatalf("stale/orphan Git registrations were exposed: %#v", result.Items)
	}
}
