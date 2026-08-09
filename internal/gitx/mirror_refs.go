package gitx

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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

// MirrorLog returns the exact oldest-to-newest bounded commit range from the
// managed mirror. It never consults the configured project worktree.
func (r Runner) MirrorLog(ctx context.Context, p config.ProjectConfig, from, to string, limit int) ([]Commit, error) {
	if err := model.ValidateRevision(from); err != nil {
		return nil, err
	}
	if err := model.ValidateRevision(to); err != nil {
		return nil, err
	}
	if limit < 1 || limit > r.MaxListItems {
		return nil, fmt.Errorf("invalid log limit")
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return nil, err
	}
	countOut, err := r.command(ctx, p.Mirror, true, "rev-list", "--count", from+".."+to)
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
	out, err := r.command(ctx, p.Mirror, true, "log", "--reverse", "--no-decorate", "--max-count="+strconv.Itoa(limit), "--format="+format, from+".."+to)
	if err != nil {
		return nil, err
	}
	if _, err := bounded(out, r.MaxReadBytes); err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	items := []Commit{}
	for i := 0; i+5 < len(parts) && len(items) < limit; i += 6 {
		sha := strings.TrimSpace(string(parts[i]))
		if sha == "" {
			continue
		}
		items = append(items, Commit{
			SHA:         sha,
			Parents:     strings.Fields(string(parts[i+1])),
			AuthorName:  string(parts[i+2]),
			AuthorEmail: string(parts[i+3]),
			AuthorDate:  string(parts[i+4]),
			Subject:     string(parts[i+5]),
		})
	}
	if len(items) != count {
		return nil, fmt.Errorf("commit range output is incomplete")
	}
	return items, nil
}

// MirrorBranchHead resolves a branch from the refreshed managed mirror. The
// remote-tracking ref is authoritative when present; the local branch ref is
// supported because a mirror clone stores fetched remote branches there.
func (r Runner) MirrorBranchHead(ctx context.Context, p config.ProjectConfig, branch string) (string, bool, error) {
	if err := model.ValidateBranch(branch); err != nil {
		return "", false, err
	}
	for _, ref := range []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch} {
		head, exists, err := r.ResolveMirrorRefStatus(ctx, p, ref)
		if err != nil {
			return "", false, err
		}
		if exists {
			return head, true, nil
		}
		listed, err := r.mirrorRefListed(ctx, p, ref)
		if err != nil {
			return "", false, err
		}
		if listed {
			return "", false, fmt.Errorf("mirror branch ref does not resolve exactly")
		}
	}
	return "", false, nil
}

func (r Runner) mirrorRefListed(ctx context.Context, p config.ProjectConfig, ref string) (bool, error) {
	if err := model.ValidateBranch(strings.TrimPrefix(strings.TrimPrefix(ref, "refs/remotes/origin/"), "refs/heads/")); err != nil {
		return false, err
	}
	if err := r.EnsureMirror(ctx, p); err != nil {
		return false, err
	}
	out, err := r.command(ctx, p.Mirror, true, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return false, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == ref {
			return true, nil
		}
	}
	return false, nil
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
	return Compare{
		MergeBase: strings.TrimSpace(string(baseOut)),
		LeftOnly:  l,
		RightOnly: rr,
	}, nil
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
