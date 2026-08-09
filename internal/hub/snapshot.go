package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const maxSnapshotAggregateReadBytes int64 = 64 << 20

// ReadSnapshot is a bounded, read-only view of one exact authoritative Hub
// revision. The managed-root lock remains held for the lifetime of the view.
type ReadSnapshot struct {
	store     Store
	root      string
	revision  string
	release   func() error
	readBytes int64
}

// ReadSnapshot opens one validated managed root and captures its remote
// revision. Callers must close the snapshot when the graph read is complete.
func (s Store) ReadSnapshot(ctx context.Context) (*ReadSnapshot, error) {
	lock, err := s.readOnlyLock()
	if err != nil {
		return nil, err
	}
	release := func() error { return lock.Release() }
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		_ = release()
		return nil, err
	}
	revision, err := s.remoteRevisionLocked(ctx, root)
	if err != nil {
		_ = release()
		return nil, err
	}
	return &ReadSnapshot{
		store:    s,
		root:     root,
		revision: revision,
		release:  release,
	}, nil
}

func (r *ReadSnapshot) Close() error {
	if r == nil || r.release == nil {
		return nil
	}
	release := r.release
	r.release = nil
	return release()
}

func (r *ReadSnapshot) Revision() string { return r.revision }

func (r *ReadSnapshot) List(ctx context.Context, prefix, suffix string) ([]string, error) {
	if err := validateHubPath(prefix); err != nil {
		return nil, err
	}
	out, err := command(ctx, r.root, "ls-tree", "-r", "--name-only", r.revision, "--", filepath.ToSlash(prefix))
	if err != nil {
		return nil, err
	}
	items := make([]string, 0)
	for _, line := range splitLines(string(out)) {
		if suffix != "" && !strings.HasSuffix(line, suffix) {
			continue
		}
		items = append(items, line)
	}
	if len(items) > r.store.Config.MaxListItems {
		return nil, fmt.Errorf("hub list exceeds configured maximum")
	}
	sort.Strings(items)
	return items, nil
}

func (r *ReadSnapshot) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := validateHubPath(path); err != nil {
		return nil, err
	}
	out, err := command(ctx, r.root, "show", r.revision+":"+filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > r.store.Config.MaxReadBytes {
		return nil, fmt.Errorf("hub file exceeds configured maximum")
	}
	if r.readBytes > maxSnapshotAggregateReadBytes-int64(len(out)) {
		return nil, fmt.Errorf("hub snapshot aggregate read exceeds configured maximum")
	}
	r.readBytes += int64(len(out))
	return out, nil
}

func (r *ReadSnapshot) ReadJSON(ctx context.Context, path string, target any) error {
	raw, err := r.ReadFile(ctx, path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON content")
		}
		return err
	}
	return nil
}

func splitLines(value string) []string {
	lines := make([]string, 0)
	for _, line := range bytes.Split([]byte(value), []byte{'\n'}) {
		if len(line) != 0 {
			lines = append(lines, string(line))
		}
	}
	return lines
}

var _ *lockfile.Lock
