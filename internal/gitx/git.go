package gitx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type Runner struct {
	MaxReadBytes int64
	MaxDiffBytes int64
	MaxListItems int
}

type Ref struct {
	Name          string `json:"name"`
	ObjectType    string `json:"object_type"`
	ObjectName    string `json:"object_name"`
	Subject       string `json:"subject,omitempty"`
	CommitterDate string `json:"committer_date,omitempty"`
}
type Commit struct {
	SHA         string   `json:"sha"`
	Parents     []string `json:"parents"`
	AuthorName  string   `json:"author_name"`
	AuthorEmail string   `json:"author_email"`
	AuthorDate  string   `json:"author_date"`
	Subject     string   `json:"subject"`
}
type WorktreeStatus struct {
	Branch    string `json:"branch"`
	Head      string `json:"head"`
	Upstream  string `json:"upstream,omitempty"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Porcelain string `json:"porcelain"`
	Clean     bool   `json:"clean"`
}
type Compare struct {
	MergeBase string `json:"merge_base"`
	LeftOnly  int    `json:"left_only"`
	RightOnly int    `json:"right_only"`
}

func (r Runner) command(ctx context.Context, dir string, gitDir bool, args ...string) ([]byte, error) {
	base := []string{"-c", "core.pager=cat", "-c", "pager.log=false", "-c", "pager.show=false", "-c", "diff.external=", "-c", "color.ui=false"}
	base = append(base, args...)
	cmd := exec.CommandContext(ctx, "git", base...)
	if gitDir {
		cmd.Env = append(cleanEnv(), "GIT_DIR="+dir)
	} else {
		cmd.Dir = dir
		cmd.Env = cleanEnv()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
func cleanEnv() []string {
	allowed := []string{"HOME", "PATH", "SSH_AUTH_SOCK", "USER", "LOGNAME", "TMPDIR"}
	out := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"}
	for _, k := range allowed {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}
func bounded(data []byte, max int64) (string, error) {
	if int64(len(data)) > max {
		return "", fmt.Errorf("git output exceeds %d bytes", max)
	}
	return string(data), nil
}
func validatePath(path string) error {
	if path == "" {
		return nil
	}
	return model.ValidateRelativePath(path)
}

func (r Runner) EnsureMirror(ctx context.Context, p config.ProjectConfig) error {
	if _, err := os.Stat(filepath.Join(p.Mirror, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.Mirror), 0o700); err != nil {
		return err
	}
	urlOut, err := r.command(ctx, p.Root, false, "remote", "get-url", p.Remote)
	if err != nil {
		return err
	}
	url := strings.TrimSpace(string(urlOut))
	if url == "" {
		return fmt.Errorf("empty project remote URL")
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--mirror", "--", url, p.Mirror)
	cmd.Env = cleanEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create mirror: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
func (r Runner) Refresh(ctx context.Context, p config.ProjectConfig) error {
	if err := r.EnsureMirror(ctx, p); err != nil {
		return err
	}
	_, err := r.command(ctx, p.Mirror, true, "remote", "update", "--prune")
	return err
}
func (r Runner) Refs(ctx context.Context, p config.ProjectConfig) ([]Ref, error) {
	if err := r.EnsureMirror(ctx, p); err != nil {
		return nil, err
	}
	format := "%(refname)%00%(objecttype)%00%(objectname)%00%(subject)%00%(committerdate:iso-strict)%00"
	out, err := r.command(ctx, p.Mirror, true, "for-each-ref", "--format="+format, "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > r.MaxReadBytes {
		return nil, fmt.Errorf("refs output too large")
	}
	parts := bytes.Split(out, []byte{0})
	refs := []Ref{}
	for i := 0; i+4 < len(parts) && len(refs) < r.MaxListItems; i += 5 {
		name := strings.TrimSpace(string(parts[i]))
		if name == "" {
			continue
		}
		refs = append(refs, Ref{Name: name, ObjectType: string(parts[i+1]), ObjectName: string(parts[i+2]), Subject: string(parts[i+3]), CommitterDate: strings.TrimSpace(string(parts[i+4]))})
	}
	return refs, nil
}
func (r Runner) Log(ctx context.Context, p config.ProjectConfig, rev string, limit int) ([]Commit, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return nil, err
	}
	if limit < 1 || limit > r.MaxListItems {
		return nil, fmt.Errorf("invalid log limit")
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return nil, err
	}
	format := "%H%x00%P%x00%an%x00%ae%x00%aI%x00%s%x00"
	out, err := r.command(ctx, p.Mirror, true, "log", "--no-decorate", "--max-count="+strconv.Itoa(limit), "--format="+format, rev)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	items := []Commit{}
	for i := 0; i+5 < len(parts) && len(items) < limit; i += 6 {
		sha := strings.TrimSpace(string(parts[i]))
		if sha == "" {
			continue
		}
		parents := strings.Fields(string(parts[i+1]))
		items = append(items, Commit{SHA: sha, Parents: parents, AuthorName: string(parts[i+2]), AuthorEmail: string(parts[i+3]), AuthorDate: string(parts[i+4]), Subject: string(parts[i+5])})
	}
	return items, nil
}

// LocalLog reads the ordered commits from a configured worktree. It is used
// for finalization because the task branch may not have been pushed yet.
func (r Runner) LocalLog(ctx context.Context, root, from, to string, limit int) ([]Commit, error) {
	if err := model.ValidateRevision(from); err != nil {
		return nil, err
	}
	if err := model.ValidateRevision(to); err != nil {
		return nil, err
	}
	if limit < 1 || limit > r.MaxListItems {
		return nil, fmt.Errorf("invalid log limit")
	}
	countOut, err := r.command(ctx, root, false, "rev-list", "--count", from+".."+to)
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOut)))
	if err != nil {
		return nil, fmt.Errorf("invalid commit count")
	}
	if count > limit {
		return nil, fmt.Errorf("commit range exceeds configured limit")
	}
	format := "%H%x00%P%x00%an%x00%ae%x00%aI%x00%s%x00"
	out, err := r.command(ctx, root, false, "log", "--reverse", "--no-decorate", "--max-count="+strconv.Itoa(limit), "--format="+format, from+".."+to)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	items := []Commit{}
	for i := 0; i+5 < len(parts) && len(items) < limit; i += 6 {
		sha := strings.TrimSpace(string(parts[i]))
		if sha == "" {
			continue
		}
		items = append(items, Commit{SHA: sha, Parents: strings.Fields(string(parts[i+1])), AuthorName: string(parts[i+2]), AuthorEmail: string(parts[i+3]), AuthorDate: string(parts[i+4]), Subject: string(parts[i+5])})
	}
	return items, nil
}
func (r Runner) Show(ctx context.Context, p config.ProjectConfig, rev string) (string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return "", err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Mirror, true, "show", "--no-ext-diff", "--no-textconv", "--format=fuller", "--stat", "--summary", rev)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxReadBytes)
}
func (r Runner) Tree(ctx context.Context, p config.ProjectConfig, rev, path string) ([]string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return nil, err
	}
	if err := validatePath(path); err != nil {
		return nil, err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return nil, err
	}
	args := []string{"ls-tree", "-r", "--name-only", rev}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.command(ctx, p.Mirror, true, args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > r.MaxListItems {
		return nil, fmt.Errorf("tree exceeds item limit")
	}
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	return lines, nil
}
func (r Runner) ReadFile(ctx context.Context, p config.ProjectConfig, rev, path string) (string, error) {
	if err := model.ValidateRevision(rev); err != nil {
		return "", err
	}
	if err := model.ValidateRelativePath(path); err != nil {
		return "", err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Mirror, true, "show", rev+":"+filepath.ToSlash(path))
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxReadBytes)
}
func (r Runner) Diff(ctx context.Context, p config.ProjectConfig, from, to string, paths []string) (string, error) {
	if err := model.ValidateRevision(from); err != nil {
		return "", err
	}
	if err := model.ValidateRevision(to); err != nil {
		return "", err
	}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return "", err
		}
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--find-copies", from, to}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := r.command(ctx, p.Mirror, true, args...)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxDiffBytes)
}
func (r Runner) ChangedFiles(ctx context.Context, root, from, to string) ([]string, error) {
	if err := model.ValidateRevision(from); err != nil {
		return nil, err
	}
	if err := model.ValidateRevision(to); err != nil {
		return nil, err
	}
	out, err := r.command(ctx, root, false, "diff", "--name-only", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	if len(lines) > r.MaxListItems {
		return nil, fmt.Errorf("changed file list exceeds item limit")
	}
	for _, path := range lines {
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
	}
	sort.Strings(lines)
	return lines, nil
}
func (r Runner) MergeBase(ctx context.Context, p config.ProjectConfig, left, right string) (string, error) {
	if err := model.ValidateRevision(left); err != nil {
		return "", err
	}
	if err := model.ValidateRevision(right); err != nil {
		return "", err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Mirror, true, "merge-base", left, right)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
func (r Runner) Compare(ctx context.Context, p config.ProjectConfig, left, right string) (Compare, error) {
	base, err := r.MergeBase(ctx, p, left, right)
	if err != nil {
		return Compare{}, err
	}
	out, err := r.command(ctx, p.Mirror, true, "rev-list", "--left-right", "--count", left+"..."+right)
	if err != nil {
		return Compare{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return Compare{}, fmt.Errorf("unexpected compare output")
	}
	l, _ := strconv.Atoi(fields[0])
	rr, _ := strconv.Atoi(fields[1])
	return Compare{MergeBase: base, LeftOnly: l, RightOnly: rr}, nil
}

// ResolveMirrorRef resolves exactly one commit-ish in a managed mirror.
func (r Runner) ResolveMirrorRef(ctx context.Context, p config.ProjectConfig, ref string) (string, error) {
	if err := model.ValidateRevision(ref); err != nil && model.ValidateBranch(strings.TrimPrefix(ref, "refs/heads/")) != nil {
		return "", err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Mirror, true, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveMirrorRefStatus distinguishes an absent canonical ref from failures
// resolving or reading the managed mirror.
func (r Runner) ResolveMirrorRefStatus(ctx context.Context, p config.ProjectConfig, ref string) (string, bool, error) {
	if err := model.ValidateRevision(ref); err != nil && model.ValidateBranch(strings.TrimPrefix(ref, "refs/heads/")) != nil {
		return "", false, err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", false, err
	}
	if isCommitSHA(ref) {
		out, err := r.command(ctx, p.Mirror, true, "rev-parse", "--verify", ref+"^{commit}")
		if err != nil {
			if strings.Contains(err.Error(), "exit status 1") || strings.Contains(err.Error(), "exit status 128") {
				return "", false, nil
			}
			return "", false, err
		}
		return strings.TrimSpace(string(out)), true, nil
	}
	_, err := r.command(ctx, p.Mirror, true, "show-ref", "--verify", "--quiet", ref)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return "", false, nil
		}
		return "", false, err
	}
	out, err := r.command(ctx, p.Mirror, true, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", true, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

func isCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (r Runner) MirrorAncestor(ctx context.Context, p config.ProjectConfig, ancestor, descendant string) (bool, error) {
	if err := model.ValidateRevision(ancestor); err != nil {
		return false, err
	}
	if err := model.ValidateRevision(descendant); err != nil {
		return false, err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return false, err
	}
	_, err := r.command(ctx, p.Mirror, true, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}

func (r Runner) MirrorChangedFiles(ctx context.Context, p config.ProjectConfig, from, to string) ([]string, error) {
	if err := model.ValidateRevision(from); err != nil {
		return nil, err
	}
	if err := model.ValidateRevision(to); err != nil {
		return nil, err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return nil, err
	}
	out, err := r.command(ctx, p.Mirror, true, "diff", "--name-only", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	if len(lines) > r.MaxListItems {
		return nil, fmt.Errorf("changed file list exceeds item limit")
	}
	for _, path := range lines {
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
	}
	sort.Strings(lines)
	return lines, nil
}

func (r Runner) MirrorCompare(ctx context.Context, p config.ProjectConfig, left, right string) (Compare, error) {
	if err := model.ValidateRevision(left); err != nil {
		return Compare{}, err
	}
	if err := model.ValidateRevision(right); err != nil {
		return Compare{}, err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return Compare{}, err
	}
	baseOut, err := r.command(ctx, p.Mirror, true, "merge-base", left, right)
	if err != nil {
		return Compare{}, err
	}
	out, err := r.command(ctx, p.Mirror, true, "rev-list", "--left-right", "--count", left+"..."+right)
	if err != nil {
		return Compare{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return Compare{}, fmt.Errorf("unexpected compare output")
	}
	l, err := strconv.Atoi(fields[0])
	if err != nil {
		return Compare{}, err
	}
	rr, err := strconv.Atoi(fields[1])
	if err != nil {
		return Compare{}, err
	}
	return Compare{MergeBase: strings.TrimSpace(string(baseOut)), LeftOnly: l, RightOnly: rr}, nil
}

func (r Runner) MirrorDiffStat(ctx context.Context, p config.ProjectConfig, from, to string) (string, error) {
	if err := model.ValidateRevision(from); err != nil {
		return "", err
	}
	if err := model.ValidateRevision(to); err != nil {
		return "", err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return "", err
	}
	out, err := r.command(ctx, p.Mirror, true, "diff", "--stat", "--summary", from, to)
	if err != nil {
		return "", err
	}
	return bounded(out, r.MaxDiffBytes)
}
func (r Runner) WorktreeStatus(ctx context.Context, p config.ProjectConfig) (WorktreeStatus, error) {
	out, err := r.command(ctx, p.Root, false, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return WorktreeStatus{}, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return WorktreeStatus{}, err
	}
	s := WorktreeStatus{Porcelain: text, Clean: true}
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

// BranchHead resolves a local branch without depending on the checked-out
// branch. A missing branch is reported separately so callers can validate
// branch identity when the ref is available without inventing one.
func (r Runner) BranchHead(ctx context.Context, root, branch string) (string, bool, error) {
	if err := model.ValidateBranch(branch); err != nil {
		return "", false, err
	}
	ref := "refs/heads/" + branch
	if _, err := r.command(ctx, root, false, "show-ref", "--verify", "--quiet", ref); err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return "", false, nil
		}
		return "", false, err
	}
	out, err := r.command(ctx, root, false, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", true, err
	}
	return strings.TrimSpace(string(out)), true, nil
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
func JSON(value any) string { b, _ := json.MarshalIndent(value, "", "  "); return string(b) }
func Timeout(seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}
