package gitx

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (r Runner) WorktreeStatus(ctx context.Context, p config.ProjectConfig) (WorktreeStatus, error) {
	out, err := r.command(ctx, p.Root, false, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return WorktreeStatus{}, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return WorktreeStatus{}, err
	}
	s := WorktreeStatus{
		Porcelain: text,
		Clean:     true,
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			s.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.oid "):
			s.Head = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			fmt.Sscanf(line, "# branch.ab +%d -%d", &s.Ahead, &s.Behind)
		case line != "" && !strings.HasPrefix(line, "# "):
			s.Clean = false
		}
	}
	return s, nil
}
func (r Runner) WorktreeDiff(ctx context.Context, p config.ProjectConfig, staged bool) (string, error) {
	args := []string{"diff", "--no-ext-diff", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	out, err := r.command(ctx, p.Root, false, args...)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxDiffBytes)
}
func (r Runner) Resolve(ctx context.Context, root, rev string) (string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return "", err
	}
	out, err := r.command(ctx, root, false, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r Runner) PrepareBranch(ctx context.Context, p config.ProjectConfig, branch, base string) error {
	if err := model.ValidateBranch(branch); err != nil {
		return err
	}
	if _, err := r.command(ctx, p.Root, false, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid Git branch: %w", err)
	}
	status, err := r.WorktreeStatus(ctx, p)
	if err != nil {
		return err
	}
	if !status.Clean {
		return fmt.Errorf("project worktree is dirty")
	}
	resolved, err := r.Resolve(ctx, p.Root, base)
	if err != nil {
		return err
	}
	if resolved != base {
		return fmt.Errorf("base revision did not resolve exactly")
	}
	_, err = r.command(ctx, p.Root, false, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		if _, e := r.command(ctx, p.Root, false, "merge-base", "--is-ancestor", base, branch); e != nil {
			return fmt.Errorf("existing branch is not based on task base")
		}
		_, err = r.command(ctx, p.Root, false, "switch", branch)
		return err
	}
	_, err = r.command(ctx, p.Root, false, "switch", "-c", branch, base)
	return err
}
func (r Runner) IsAncestor(ctx context.Context, root, ancestor, descendant string) (bool, error) {
	if err := model.ValidateRevision(ancestor); err != nil {
		return false, err
	}
	if err := model.ValidateRevision(descendant); err != nil {
		return false, err
	}
	_, err := r.command(ctx, root, false, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}

func (r Runner) CurrentHead(ctx context.Context, p config.ProjectConfig) (string, string, bool, error) {
	s, err := r.WorktreeStatus(ctx, p)
	return s.Head, s.Branch, s.Clean, err
}

// TreeID returns the exact Git content tree for the current HEAD. Commit
// identity is intentionally not part of this value so equivalent trees can
// be recognized across commits.
func (r Runner) TreeID(ctx context.Context, p config.ProjectConfig) (string, error) {
	out, err := r.command(ctx, p.Root, false, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if err := model.ValidateRevision(tree); err != nil {
		return "", fmt.Errorf("invalid Git tree identity: %w", err)
	}
	return tree, nil
}

// WorktreeContentID returns the prospective Git tree object for the bytes
// currently visible in the worktree. It is calculated without changing the
// index, so a dirty worktree and the commit made from those same bytes have
// the same identity.
func (r Runner) WorktreeContentID(ctx context.Context, p config.ProjectConfig) (string, error) {
	tracked, err := r.command(ctx, p.Root, false, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	headPaths, err := r.command(ctx, p.Root, false, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return "", err
	}
	paths := map[string]struct{}{}
	for _, raw := range append(bytesSplitNUL(tracked), bytesSplitLines(headPaths)...) {
		if raw == "" {
			continue
		}
		if err := model.ValidateRelativePath(raw); err != nil {
			return "", err
		}
		paths[filepath.FromSlash(raw)] = struct{}{}
	}
	root := prospectiveTreeNode{
		entries: map[string]prospectiveTreeEntry{},
		dirs:    map[string]*prospectiveTreeNode{},
	}
	for path := range paths {
		full := filepath.Join(p.Root, path)
		info, err := os.Lstat(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		mode := "100644"
		var content []byte
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return "", err
			}
			mode = "120000"
			content = []byte(target)
		case info.Mode().IsRegular():
			file, err := os.Open(full)
			if err != nil {
				return "", err
			}
			content, err = io.ReadAll(file)
			closeErr := file.Close()
			if err != nil {
				return "", err
			}
			if closeErr != nil {
				return "", closeErr
			}
			if info.Mode().Perm()&0o111 != 0 {
				mode = "100755"
			}
		default:
			return "", fmt.Errorf("unsupported worktree entry %s", path)
		}
		blob := gitObjectID("blob", content)
		parts := strings.Split(filepath.ToSlash(path), "/")
		node := &root
		for _, part := range parts[:len(parts)-1] {
			if _, exists := node.entries[part]; exists {
				return "", fmt.Errorf("worktree path conflicts with file: %s", path)
			}
			child := node.dirs[part]
			if child == nil {
				child = &prospectiveTreeNode{
					entries: map[string]prospectiveTreeEntry{},
					dirs:    map[string]*prospectiveTreeNode{},
				}
				node.dirs[part] = child
			}
			node = child
		}
		name := parts[len(parts)-1]
		if _, exists := node.dirs[name]; exists {
			return "", fmt.Errorf("worktree path conflicts with directory: %s", path)
		}
		node.entries[name] = prospectiveTreeEntry{
			mode:   mode,
			object: blob,
		}
	}
	return hex.EncodeToString(root.objectID()), nil
}

type prospectiveTreeNode struct {
	entries map[string]prospectiveTreeEntry
	dirs    map[string]*prospectiveTreeNode
}

type prospectiveTreeEntry struct {
	mode   string
	object [sha1.Size]byte
}

func (n *prospectiveTreeNode) objectID() [sha1.Size]byte {
	type namedEntry struct {
		name string
		mode string
		id   [sha1.Size]byte
		key  string
	}
	entries := make([]namedEntry, 0, len(n.entries)+len(n.dirs))
	for name, entry := range n.entries {
		entries = append(entries, namedEntry{
			name: name,
			mode: entry.mode,
			id:   entry.object,
			key:  name,
		})
	}
	for name, child := range n.dirs {
		entries = append(entries, namedEntry{
			name: name,
			mode: "40000",
			id:   child.objectID(),
			key:  name + "/",
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	body := make([]byte, 0)
	for _, entry := range entries {
		body = append(body, entry.mode...)
		body = append(body, ' ')
		body = append(body, entry.name...)
		body = append(body, 0)
		body = append(body, entry.id[:]...)
	}
	return gitObjectID("tree", body)
}

func gitObjectID(kind string, content []byte) [sha1.Size]byte {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "%s %d\x00", kind, len(content))
	_, _ = h.Write(content)
	var result [sha1.Size]byte
	copy(result[:], h.Sum(nil))
	return result
}

func bytesSplitNUL(data []byte) []string {
	parts := strings.Split(string(data), "\x00")
	return parts
}

func bytesSplitLines(data []byte) []string {
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
