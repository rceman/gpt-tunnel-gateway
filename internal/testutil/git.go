package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var repoTemplate struct {
	sync.Once
	bare string
	work string
	head string
	err  error
}

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
	repoTemplate.Do(func() {
		root, err := os.MkdirTemp("", "gpt-tunnel-repo-template-")
		if err != nil {
			repoTemplate.err = err
			return
		}
		repoTemplate.bare = filepath.Join(root, "remote.git")
		if err := os.Mkdir(repoTemplate.bare, 0o700); err != nil {
			repoTemplate.err = err
			return
		}
		Git(t, root, "init", "--bare", repoTemplate.bare)
		repoTemplate.work = filepath.Join(root, "work")
		Git(t, root, "clone", repoTemplate.bare, repoTemplate.work)
		Git(t, repoTemplate.work, "config", "user.email", "test@example.invalid")
		Git(t, repoTemplate.work, "config", "user.name", "Test")
		if err := os.WriteFile(filepath.Join(repoTemplate.work, "README.md"), []byte("base\n"), 0o600); err != nil {
			repoTemplate.err = err
			return
		}
		Git(t, repoTemplate.work, "add", "README.md")
		Git(t, repoTemplate.work, "commit", "-m", "base")
		Git(t, repoTemplate.work, "branch", "-M", "main")
		Git(t, repoTemplate.work, "push", "-u", "origin", "main")
		Git(t, repoTemplate.bare, "symbolic-ref", "HEAD", "refs/heads/main")
		repoTemplate.head = trim(Git(t, repoTemplate.work, "rev-parse", "HEAD"))
	})
	if repoTemplate.err != nil {
		t.Fatal(repoTemplate.err)
	}
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	copyDir(t, repoTemplate.bare, bare)
	copyDir(t, repoTemplate.work, work)
	configPath := filepath.Join(work, ".git", "config")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.ReplaceAll(string(config), repoTemplate.bare, bare))
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	return bare, work, repoTemplate.head
}

func copyDir(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
}
func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
