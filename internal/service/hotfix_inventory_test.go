package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestHotfixListAndReadUseBoundedIdentityAuthority(t *testing.T) {
	s, _, base := testServiceWithoutIdentifiers(t)
	if err := s.Git.EnsureMirror(context.Background(), s.Config.Projects["example"]); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	slug := "newer"
	branch := "hotfix/" + slug
	lane := filepath.Join(s.Config.StateDir, "hotfix-worktrees", "example", slug)
	if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, s.Config.Projects["example"].Root, "branch", branch, base)
	testutil.Git(t, s.Config.Projects["example"].Root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, s.Config.Projects["example"].Root, "worktree", "remove", "--force", lane)
		testutil.Git(t, s.Config.Projects["example"].Root, "branch", "-D", branch)
	})
	if err := os.WriteFile(filepath.Join(lane, "marker.txt"), []byte("materialized\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	longSubject := strings.Repeat("Ж", 100)
	testutil.Git(t, lane, "add", "marker.txt")
	testutil.Git(t, lane, "commit", "-m", longSubject)
	hotfixHead := strings.TrimSpace(testutil.Git(t, lane, "rev-parse", "HEAD"))
	testutil.Git(t, lane, "push", "origin", branch)
	if err := s.Git.Refresh(context.Background(), s.Config.Projects["example"]); err != nil {
		t.Fatal(err)
	}
	for i, slug := range []string{"older", "newer"} {
		if err := s.Git.RecordHotfixIdentity(s.Config.StateDir, gitx.HotfixIdentity{
			ProjectID: "example", HotfixRef: "refs/heads/hotfix/" + slug,
			TaskID: fmt.Sprintf("EXM-TSK%d", i+1), BaseSHA: base,
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.HotfixList(context.Background(), HotfixListInput{ProjectID: "example", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.MainHead != base[:8] || len(first.Hotfixes) != 1 || first.Hotfixes[0].Hotfix != "hotfix/newer" || first.Hotfixes[0].Head != hotfixHead[:8] || len(first.Hotfixes[0].Subject) > 160 || !utf8.ValidString(first.Hotfixes[0].Subject) || first.Hotfixes[0].Subject != strings.Repeat("Ж", 80) || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page=%#v", first)
	}
	second, err := s.HotfixList(context.Background(), HotfixListInput{ProjectID: "example", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Hotfixes) != 1 || second.Hotfixes[0].Hotfix != "hotfix/older" || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second page=%#v", second)
	}
	read, err := s.HotfixRead(context.Background(), HotfixReadInput{ProjectID: "example", Hotfix: "hotfix/newer"})
	if err != nil {
		t.Fatal(err)
	}
	if read.ProjectID != "example" || read.HotfixRef != "refs/heads/hotfix/newer" || read.TaskID != "EXM-TSK2" || read.BaseSHA != base || !read.Materialized || read.HeadSHA != hotfixHead || len(read.HeadSHA) != 40 {
		t.Fatalf("read=%#v", read)
	}
	if _, err := s.HotfixRead(context.Background(), HotfixReadInput{ProjectID: "example", Hotfix: "hotfix/missing"}); err == nil {
		t.Fatal("missing hotfix identity unexpectedly succeeded")
	}
}
