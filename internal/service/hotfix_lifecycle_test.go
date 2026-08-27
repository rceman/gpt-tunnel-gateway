package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestHotfixLifecycleUsesRecordedBaseAndExactRetryIsNoOp(t *testing.T) {
	_, work, base := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	s := New(config.Config{
		StateDir:     stateDir,
		MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100,
		Projects: map[string]config.ProjectConfig{"example": {
			Root: work, Mirror: filepath.Join(stateDir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "test_master",
		}},
	})
	created, err := s.HotfixCreate(context.Background(), "example", HotfixCreateInput{Slug: "repair"})
	if err != nil {
		t.Fatal(err)
	}
	if created.BaseSHA != base || created.HeadSHA != base {
		t.Fatalf("created=%#v want base=%s", created, base)
	}
	if _, err := s.HotfixIntegrate(context.Background(), "example", HotfixIntegrateInput{HotfixRef: created.HotfixRef, ReviewedSHA: created.BaseSHA}); err == nil {
		t.Fatal("reviewed base was accepted as an integration commit")
	}
	laneRoot := filepath.Join(stateDir, "hotfix-worktrees", "example", "repair")
	if err := os.WriteFile(filepath.Join(laneRoot, "fix.txt"), []byte("fix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, laneRoot, "add", "fix.txt")
	testutil.Git(t, laneRoot, "commit", "-m", "hotfix")
	reviewed := strings.TrimSpace(testutil.Git(t, laneRoot, "rev-parse", "HEAD"))
	input := HotfixIntegrateInput{HotfixRef: created.HotfixRef, ReviewedSHA: reviewed}
	first, err := s.HotfixIntegrate(context.Background(), "example", input)
	if err != nil {
		t.Fatal(err)
	}
	if first.MainAfter != reviewed || first.BaseSHA != base {
		t.Fatalf("first integration=%#v", first)
	}
	second, err := s.HotfixIntegrate(context.Background(), "example", input)
	if err != nil {
		t.Fatal(err)
	}
	if second.MainBefore != reviewed || second.MainAfter != reviewed || second.BaseSHA != base {
		t.Fatalf("retry integration=%#v", second)
	}
}
