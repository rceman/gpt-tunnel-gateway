package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func newDefaultBranchSyncFixture(t *testing.T) (Runner, string, string, string) {
	t.Helper()
	bare, source, oldHead := testutil.RepoWithBareRemote(t)
	writer := filepath.Join(t.TempDir(), "writer")
	testutil.Git(t, filepath.Dir(writer), "clone", bare, writer)
	testutil.Git(t, writer, "config", "user.email", "test@example.invalid")
	testutil.Git(t, writer, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(writer, "canonical.txt"), []byte("canonical\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, writer, "add", "canonical.txt")
	testutil.Git(t, writer, "commit", "-m", "advance canonical main")
	testutil.Git(t, writer, "push", "origin", "main")
	state := t.TempDir()
	project := hotfixTestProject(source, filepath.Join(state, "mirror.git"))
	runner := hotfixTestRunner(state)
	canonical, err := runner.RefreshDefaultBranch(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if canonical == oldHead {
		t.Fatal("canonical branch did not advance")
	}
	return runner, source, canonical, oldHead
}

func TestSynchronizeDefaultBranchWorktreeStrictFastForwardAndIdempotence(t *testing.T) {
	runner, source, canonical, oldHead := newDefaultBranchSyncFixture(t)
	project := hotfixTestProject(source, filepath.Join(runner.StateDir, "mirror.git"))
	if _, err := runner.Resolve(context.Background(), source, canonical); err == nil {
		t.Fatal("source unexpectedly had canonical object before synchronization")
	}
	status, err := runner.SynchronizeDefaultBranchWorktree(context.Background(), project, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if status.Head != canonical || status.Branch != "main" || !status.Clean {
		t.Fatalf("synchronized status=%#v", status)
	}
	content, err := os.ReadFile(filepath.Join(source, "canonical.txt"))
	if err != nil || string(content) != "canonical\n" {
		t.Fatalf("canonical bytes=%q err=%v", content, err)
	}
	repeated, err := runner.SynchronizeDefaultBranchWorktree(context.Background(), project, canonical)
	if err != nil || repeated.Head != canonical || repeated.Branch != "main" || !repeated.Clean {
		t.Fatalf("idempotent synchronization status=%#v err=%v", repeated, err)
	}
	got := strings.TrimSpace(testutil.Git(t, source, "rev-parse", "HEAD"))
	if got != canonical {
		t.Fatalf("HEAD=%q want %q (old=%q)", got, canonical, oldHead)
	}
}

func TestSynchronizeDefaultBranchWorktreeRejectsDirtyWrongBranchAndDivergence(t *testing.T) {
	t.Run("dirty", func(t *testing.T) {
		runner, source, canonical, _ := newDefaultBranchSyncFixture(t)
		project := hotfixTestProject(source, filepath.Join(t.TempDir(), "mirror.git"))
		if err := os.WriteFile(filepath.Join(source, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := strings.TrimSpace(testutil.Git(t, source, "rev-parse", "HEAD"))
		if _, err := runner.SynchronizeDefaultBranchWorktree(context.Background(), project, canonical); err == nil {
			t.Fatal("dirty default branch was synchronized")
		}
		if after := strings.TrimSpace(testutil.Git(t, source, "rev-parse", "HEAD")); after != before {
			t.Fatalf("dirty rejection moved HEAD from %s to %s", before, after)
		}
	})
	t.Run("wrong branch", func(t *testing.T) {
		runner, source, canonical, _ := newDefaultBranchSyncFixture(t)
		project := hotfixTestProject(source, filepath.Join(t.TempDir(), "mirror.git"))
		testutil.Git(t, source, "switch", "-c", "feature")
		if _, err := runner.SynchronizeDefaultBranchWorktree(context.Background(), project, canonical); err == nil {
			t.Fatal("wrong branch was synchronized")
		}
	})
	t.Run("diverged", func(t *testing.T) {
		runner, source, canonical, _ := newDefaultBranchSyncFixture(t)
		project := hotfixTestProject(source, filepath.Join(t.TempDir(), "mirror.git"))
		if err := os.WriteFile(filepath.Join(source, "diverged.txt"), []byte("diverged\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, source, "add", "diverged.txt")
		testutil.Git(t, source, "commit", "-m", "diverge local main")
		if _, err := runner.SynchronizeDefaultBranchWorktree(context.Background(), project, canonical); err == nil {
			t.Fatal("diverged default branch was synchronized")
		}
	})
}
