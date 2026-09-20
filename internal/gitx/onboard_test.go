package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestBootstrapEmptyCreatesOnlyDeterministicReadmeOnMain(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	testutil.Git(t, root, "init", "--bare", bare)
	testutil.Git(t, root, "clone", bare, work)
	runner := Runner{
		MaxReadBytes: 1 << 20,
		StateDir:     root,
	}
	project := config.ProjectConfig{Root: work, Remote: "origin"}
	if hasCommit, err := runner.RepositoryHasCommit(context.Background(), project); err != nil || hasCommit {
		t.Fatalf("empty repository state hasCommit=%v err=%v", hasCommit, err)
	}
	if err := runner.BootstrapEmpty(context.Background(), project, "# widget\n"); err != nil {
		t.Fatal(err)
	}
	status, err := runner.WorktreeStatus(context.Background(), project)
	if err != nil || !status.Clean || status.Branch != "main" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("working tree entries=%d, want .git and README.md", len(entries))
	}
	if data, err := os.ReadFile(filepath.Join(work, "README.md")); err != nil || string(data) != "# widget\n" {
		t.Fatalf("README=%q err=%v", data, err)
	}
	remoteReadme := testutil.Git(t, bare, "show", "main:README.md")
	if remoteReadme != "# widget\n" {
		t.Fatalf("remote README=%q", remoteReadme)
	}
	if got := testutil.Git(t, bare, "ls-tree", "--name-only", "main"); got != "README.md\n" {
		t.Fatalf("remote tree=%q", got)
	}
}
