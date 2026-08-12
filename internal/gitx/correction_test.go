package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestChangedFilesUsesCommittedDiff(t *testing.T) {
	_, work, base := testutil.RepoWithBareRemote(t)
	if err := os.WriteFile(filepath.Join(work, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "new.txt")
	testutil.Git(t, work, "commit", "-m", "new")
	head := testutil.Git(t, work, "rev-parse", "HEAD")
	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	files, err := r.ChangedFiles(context.Background(), work, base, stringTrim(head))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "new.txt" {
		t.Fatalf("%#v", files)
	}
}

func TestReplayTrainCommitsRebasesOwnedLaneAndReturnsMapping(t *testing.T) {
	_, work, base := testutil.RepoWithBareRemote(t)
	testutil.Git(t, work, "switch", "-c", "train/GTW-TRN1")
	if err := os.WriteFile(filepath.Join(work, "lane.txt"), []byte("lane\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "lane.txt")
	testutil.Git(t, work, "commit", "-m", "lane")
	laneCommit := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD"))
	testutil.Git(t, work, "switch", "main")
	if err := os.WriteFile(filepath.Join(work, "target.txt"), []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "target.txt")
	testutil.Git(t, work, "commit", "-m", "target")
	target := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD"))
	testutil.Git(t, work, "switch", "train/GTW-TRN1")

	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	head, mapping, err := r.ReplayTrainCommits(context.Background(), config.ProjectConfig{Root: work}, target, []string{laneCommit})
	if err != nil {
		t.Fatal(err)
	}
	if mapping[laneCommit] != head || head == laneCommit {
		t.Fatalf("unexpected replay mapping: head=%q mapping=%#v", head, mapping)
	}
	if got := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD")); got != head || stringTrim(testutil.Git(t, work, "status", "--porcelain")) != "" {
		t.Fatalf("replayed lane is not clean at returned head: head=%s", got)
	}
	if _, err := os.Stat(filepath.Join(work, "lane.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "target.txt")); err != nil {
		t.Fatal(err)
	}
	if head == base {
		t.Fatal("replay unexpectedly returned the original base")
	}
}

func TestReplayTrainCommitsConflictRestoresOriginalLane(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, work, "switch", "-c", "train/GTW-TRN1")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("lane\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "README.md")
	testutil.Git(t, work, "commit", "-m", "lane conflict")
	laneHead := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD"))
	testutil.Git(t, work, "switch", "main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "README.md")
	testutil.Git(t, work, "commit", "-m", "target conflict")
	target := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD"))
	testutil.Git(t, work, "switch", "train/GTW-TRN1")

	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	if _, _, err := r.ReplayTrainCommits(context.Background(), config.ProjectConfig{Root: work}, target, []string{laneHead}); err == nil {
		t.Fatal("conflicting replay unexpectedly succeeded")
	}
	if got := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD")); got != laneHead {
		t.Fatalf("conflict did not restore original lane head: got=%s want=%s", got, laneHead)
	}
	if got := stringTrim(testutil.Git(t, work, "show", "HEAD:README.md")); got != "lane" {
		t.Fatalf("conflict rollback changed original lane content: %q", got)
	}
	if got := stringTrim(testutil.Git(t, work, "status", "--porcelain")); got != "" {
		t.Fatalf("conflict rollback left dirty worktree: %q", got)
	}
}

func TestResetTrainWorktreeDiscardsReplayBeforeRestart(t *testing.T) {
	_, work, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, work, "switch", "-c", "train/GTW-TRN1")
	if err := os.WriteFile(filepath.Join(work, "lane.txt"), []byte("lane\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "lane.txt")
	testutil.Git(t, work, "commit", "-m", "lane")
	laneCommit := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD"))
	testutil.Git(t, work, "switch", "main")
	if err := os.WriteFile(filepath.Join(work, "target.txt"), []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "target.txt")
	testutil.Git(t, work, "commit", "-m", "target")
	target := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD"))
	testutil.Git(t, work, "switch", "train/GTW-TRN1")

	r := Runner{
		MaxReadBytes: 1 << 20,
		MaxDiffBytes: 1 << 20,
		MaxListItems: 100,
	}
	if _, _, err := r.ReplayTrainCommits(context.Background(), config.ProjectConfig{Root: work}, target, []string{laneCommit}); err != nil {
		t.Fatal(err)
	}
	if err := r.ResetTrainWorktree(context.Background(), config.ProjectConfig{Root: work}, target); err != nil {
		t.Fatal(err)
	}
	if got := stringTrim(testutil.Git(t, work, "rev-parse", "HEAD")); got != target {
		t.Fatalf("replay was not discarded: got=%s want=%s", got, target)
	}
	if _, err := os.Stat(filepath.Join(work, "lane.txt")); !os.IsNotExist(err) {
		t.Fatalf("discarded replay left lane file: %v", err)
	}
	if got := stringTrim(testutil.Git(t, work, "status", "--porcelain")); got != "" {
		t.Fatalf("reset left dirty lane: %q", got)
	}
}

func stringTrim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
