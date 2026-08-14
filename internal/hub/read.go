package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s Store) readOnlyLock() (*lockfile.Lock, error) {
	lock, err := lockfile.AcquireReadOnly(filepath.Join(s.Config.StateDir, "locks"), "hub-repository")
	if err != nil {
		return nil, errors.New("read-only hub lock unavailable")
	}
	return lock, nil
}

func (s Store) readOnlyRoot(ctx context.Context) (string, error) {
	root := ManagedRoot(s.Config)
	if err := s.validateManagedRoot(ctx, root); err != nil {
		return "", errors.New("read-only hub unavailable")
	}
	if _, err := command(ctx, root, "rev-parse", "--verify", s.remoteRef()+"^{commit}"); err != nil {
		return "", errors.New("read-only hub branch unavailable")
	}
	return root, nil
}
func (s Store) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := validateHubPath(path); err != nil {
		return nil, err
	}
	if snapshot := readSnapshotFromContext(ctx); snapshot != nil {
		return snapshot.ReadFile(ctx, path)
	}
	snapshot, err := s.FreshReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	return snapshot.ReadFile(ctx, path)
}

// ReadFileAtCommit reads immutable Hub history without changing the managed
// worktree or remote ref. It is used only by source-owned migrations that must
// distinguish reused identifiers across historical lineages.
func (s Store) ReadFileAtCommit(ctx context.Context, commit, path string) ([]byte, error) {
	if err := validateHubPath(path); err != nil {
		return nil, err
	}
	if err := model.ValidateCommitSHA(commit); err != nil {
		return nil, err
	}
	lock, err := s.readOnlyLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := command(ctx, root, "ls-tree", "-r", "--name-only", commit, "--", filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(entries)) == "" {
		return nil, fmt.Errorf("hub path %s at %s: %w", path, commit, os.ErrNotExist)
	}
	out, err := command(ctx, root, "show", commit+":"+filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > s.Config.MaxReadBytes {
		return nil, fmt.Errorf("hub file exceeds read limit")
	}
	return out, nil
}
func (s Store) ReadJSON(ctx context.Context, path string, out any) error {
	data, err := s.ReadFile(ctx, path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("hub JSON has trailing content")
	}
	return nil
}
func (s Store) List(ctx context.Context, prefix, suffix string) ([]string, error) {
	if err := validateHubPath(prefix); err != nil {
		return nil, err
	}
	if snapshot := readSnapshotFromContext(ctx); snapshot != nil {
		return snapshot.List(ctx, prefix, suffix)
	}
	snapshot, err := s.FreshReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	return snapshot.List(ctx, prefix, suffix)
}
func (s Store) History(ctx context.Context, path string, limit int) ([]map[string]string, error) {
	if err := validateHubPath(path); err != nil {
		return nil, err
	}
	if limit < 1 || limit > s.Config.MaxListItems {
		return nil, fmt.Errorf("invalid history limit")
	}
	lock, err := s.readOnlyLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	root, err := s.readOnlyRoot(ctx)
	if err != nil {
		return nil, err
	}
	format := "%H%x00%aI%x00%an%x00%s%x00"
	out, err := command(ctx, root, "log", "--max-count", fmt.Sprint(limit), "--format="+format, s.remoteRef(), "--", path)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	items := []map[string]string{}
	for i := 0; i+3 < len(parts) && len(items) < limit; i += 4 {
		sha := strings.TrimSpace(string(parts[i]))
		if sha == "" {
			continue
		}
		items = append(items, map[string]string{"sha": sha, "date": string(parts[i+1]), "author": string(parts[i+2]), "subject": string(parts[i+3])})
	}
	return items, nil
}
func (s Store) LastChange(ctx context.Context, path string) (string, error) {
	history, err := s.History(ctx, path, 1)
	if err != nil {
		return "", err
	}
	if len(history) != 1 || history[0]["sha"] == "" {
		return "", fmt.Errorf("no history for %s: %w", path, os.ErrNotExist)
	}
	return history[0]["sha"], nil
}
