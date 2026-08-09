package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

func stringTrim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
