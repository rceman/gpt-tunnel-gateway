package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func sourceRoot() (string, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	root := wd
	for {
		if _, e := os.Lstat(filepath.Join(root, ".git")); e == nil {
			break
		}
		p := filepath.Dir(root)
		if p == root {
			return "", "", fmt.Errorf("not inside a Git worktree")
		}
		root = p
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return "", "", fmt.Errorf("source root must not be symlinked")
	}
	sha, err := runGit(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	return root, sha, nil
}
func validateSource(root, sha string) error {
	if filepath.Base(root) != "gpt-tunnel-gateway" {
		return fmt.Errorf("unexpected repository root")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		return fmt.Errorf("invalid source SHA")
	}
	branch, err := runGit(root, "branch", "--show-current")
	if err != nil || branch != "main" {
		return fmt.Errorf("source must be on main")
	}
	remote, err := runGit(root, "remote", "get-url", "origin")
	if err != nil || remote != "git@github.com:rceman/gpt-tunnel-gateway.git" {
		return fmt.Errorf("unexpected repository identity")
	}
	clean, err := runGit(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if clean != "" {
		return fmt.Errorf("source worktree is dirty")
	}
	origin, err := runGit(root, "rev-parse", "refs/remotes/origin/main")
	if err != nil {
		return err
	}
	if origin != sha {
		return fmt.Errorf("source is not synchronized with origin/main")
	}
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return err
	}
	if !semverRE.MatchString(strings.TrimSpace(string(b))) {
		return fmt.Errorf("invalid source VERSION")
	}
	return nil
}
func buildRelease(ctx context.Context, root, dir string) error {
	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts", "build-release.sh"), dir)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if len(message) > 240 {
			message = message[:240]
		}
		return fmt.Errorf("release build failed: %s", message)
	}
	return nil
}
func validateRelease(dir, target string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := []string{}
	allowed := map[string]bool{"gpt-tunnel": true, "gpt-tunnel-gatewayd": true, "gpt-tunnelctl": true, "SHA256SUMS": true}
	for _, e := range entries {
		names = append(names, e.Name())
		if !allowed[e.Name()] {
			return fmt.Errorf("unexpected release artifact %s", e.Name())
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("release symlink")
		}
		info, er := e.Info()
		if er != nil || !info.Mode().IsRegular() || (e.Name() != "SHA256SUMS" && info.Mode()&0o111 == 0) {
			return fmt.Errorf("invalid release artifact")
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "SHA256SUMS,gpt-tunnel,gpt-tunnel-gatewayd,gpt-tunnelctl" {
		return fmt.Errorf("release output set mismatch")
	}
	lines, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	manifest := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f[0]) || strings.Contains(f[1], "/") || strings.Contains(f[1], "\\") || !allowed[f[1]] || f[1] == "SHA256SUMS" || manifest[f[1]] {
			return fmt.Errorf("invalid checksum manifest")
		}
		manifest[f[1]] = true
		data, e := os.ReadFile(filepath.Join(dir, f[1]))
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f[0] {
			return fmt.Errorf("checksum mismatch")
		}
	}
	if len(manifest) != 3 {
		return fmt.Errorf("checksum manifest is incomplete")
	}
	for _, name := range []string{"gpt-tunnel", "gpt-tunnel-gatewayd", "gpt-tunnelctl"} {
		v, e := installedVersion(filepath.Join(dir, name))
		if e != nil || v != target {
			return fmt.Errorf("release version mismatch")
		}
	}
	return nil
}
