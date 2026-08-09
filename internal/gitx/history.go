package gitx

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
		refs = append(refs, Ref{
			Name:          name,
			ObjectType:    string(parts[i+1]),
			ObjectName:    string(parts[i+2]),
			Subject:       string(parts[i+3]),
			CommitterDate: strings.TrimSpace(string(parts[i+4])),
		})
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
		items = append(items, Commit{
			SHA:         sha,
			Parents:     parents,
			AuthorName:  string(parts[i+2]),
			AuthorEmail: string(parts[i+3]),
			AuthorDate:  string(parts[i+4]),
			Subject:     string(parts[i+5]),
		})
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
		items = append(items, Commit{
			SHA:         sha,
			Parents:     strings.Fields(string(parts[i+1])),
			AuthorName:  string(parts[i+2]),
			AuthorEmail: string(parts[i+3]),
			AuthorDate:  string(parts[i+4]),
			Subject:     string(parts[i+5]),
		})
	}
	return items, nil
}
