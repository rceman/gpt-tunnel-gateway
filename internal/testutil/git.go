package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
func RepoWithBareRemote(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	Git(t, root, "init", "--bare", bare)
	work := filepath.Join(root, "work")
	Git(t, root, "clone", bare, work)
	Git(t, work, "config", "user.email", "test@example.invalid")
	Git(t, work, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Git(t, work, "add", "README.md")
	Git(t, work, "commit", "-m", "base")
	Git(t, work, "branch", "-M", "main")
	Git(t, work, "push", "-u", "origin", "main")
	Git(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	head := trim(Git(t, work, "rev-parse", "HEAD"))
	return bare, work, head
}
func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
