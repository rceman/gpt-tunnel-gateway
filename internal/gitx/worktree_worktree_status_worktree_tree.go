package gitx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *diffLineStream) write(chunk []byte) error {
	for len(chunk) > 0 {
		index := bytes.IndexByte(chunk, '\n')
		part := chunk
		if index >= 0 {
			part = chunk[:index+1]
		}
		if s.maxPendingBytes > 0 && int64(len(s.pending)+len(part)) > s.maxPendingBytes {
			return fmt.Errorf("diff semantic line exceeds internal byte safety limit of %d", s.maxPendingBytes)
		}
		s.pending = append(s.pending, part...)
		if index < 0 {
			return nil
		}
		chunk = chunk[index+1:]
		line := s.pending
		s.pending = nil
		if err := s.consume(line); err != nil {
			return err
		}
	}
	return nil
}

func (s *diffLineStream) finish() error {
	if len(s.pending) == 0 {
		return nil
	}
	line := s.pending
	s.pending = nil
	return s.consume(line)
}

func (r Runner) streamDiffCommand(ctx context.Context, dir string, args []string, stream *diffLineStream) error {
	exitCode, err := r.streamCommand(ctx, dir, false, args, stream.write)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("git diff exited with status %d", exitCode)
	}
	if stream.stopped {
		return nil
	}
	if err := stream.finish(); err != nil && !errors.Is(err, ErrStreamLimit) {
		return err
	}
	return nil
}

func (r Runner) finishDiffStream(stream *diffLineStream) (bool, error) {
	if stream.offset > stream.total {
		return false, fmt.Errorf("diff continuation cursor exceeds diff output")
	}
	return stream.stopped, nil
}

// VisitDiffLocalCommits streams a committed diff and stops at the visitor's
// semantic page boundary. It never uses a line or byte limit as pagination.
func (r Runner) VisitDiffLocalCommits(ctx context.Context, p config.ProjectConfig, from, to string, paths []string, offset int64, visit DiffLineVisitor) (bool, error) {
	if err := model.ValidateCommitSHA(from); err != nil {
		return false, err
	}
	if err := model.ValidateCommitSHA(to); err != nil {
		return false, err
	}
	if offset < 0 || visit == nil {
		return false, fmt.Errorf("invalid diff stream")
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", from, to, "--"}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return false, err
		}
		args = append(args, path)
	}
	stream := &diffLineStream{
		offset:          offset,
		visitor:         visit,
		maxPendingBytes: r.MaxDiffBytes,
	}
	if err := r.streamDiffCommand(ctx, p.Root, args, stream); err != nil {
		return false, err
	}
	return r.finishDiffStream(stream)
}

// VisitDiffWorkingFromBase streams tracked changes and regular non-ignored
// untracked files, stopping at the visitor's semantic page boundary.
func (r Runner) VisitDiffWorkingFromBase(ctx context.Context, p config.ProjectConfig, base string, paths []string, offset int64, visit DiffLineVisitor) (bool, error) {
	if err := model.ValidateCommitSHA(base); err != nil {
		return false, err
	}
	if offset < 0 || visit == nil {
		return false, fmt.Errorf("invalid diff stream")
	}
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return false, err
		}
	}
	stream := &diffLineStream{
		offset:          offset,
		visitor:         visit,
		maxPendingBytes: r.MaxDiffBytes,
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--find-copies", base}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	if err := r.streamDiffCommand(ctx, p.Root, args, stream); err != nil {
		return false, err
	}
	if stream.stopped {
		return true, nil
	}
	selected := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		selected[path] = struct{}{}
	}
	untrackedWalk, err := r.commandRecords(ctx, p.Root, false, 0, "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	if err != nil {
		return false, err
	}
	if err := untrackedWalk(func(path string) error {
		if stream.stopped {
			return ErrStreamLimit
		}
		if path == "" || (len(selected) > 0 && !pathSetContains(selected, path)) {
			return nil
		}
		if err := model.ValidateRelativePath(path); err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Join(p.Root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		code, err := r.streamCommand(ctx, p.Root, false, []string{"diff", "--no-ext-diff", "--no-textconv", "--no-index", "--", "/dev/null", filepath.ToSlash(path)}, stream.write)
		if err != nil {
			return err
		}
		if code != 0 && code != 1 {
			return fmt.Errorf("git diff --no-index exited with status %d", code)
		}
		if !stream.stopped {
			if err := stream.finish(); err != nil && !errors.Is(err, ErrStreamLimit) {
				return err
			}
		}
		if stream.stopped {
			return ErrStreamLimit
		}
		return nil
	}); err != nil && !errors.Is(err, ErrStreamLimit) {
		return false, err
	}
	return r.finishDiffStream(stream)
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

// ChangedWorkingFiles returns the bounded tracked/untracked paths in the
// working tree. It is a typed status operation used by verification scope
// resolution; callers never construct Git commands.
func (r Runner) ChangedWorkingFiles(ctx context.Context, root string) ([]string, error) {
	out, err := r.command(ctx, root, false, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	text, err := bounded(out, r.MaxReadBytes)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		path := line
		if len(path) > 3 {
			path = strings.TrimSpace(path[3:])
		}
		if arrow := strings.LastIndex(path, " -> "); arrow >= 0 {
			path = strings.TrimSpace(path[arrow+4:])
		}
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// WorktreeFingerprint hashes the exact current worktree state, including
// tracked edits, staged edits, untracked files, deletions, and file modes.
// It is deliberately content-based so a completed verification cannot be
// reused after source bytes change.
func (r Runner) WorktreeFingerprint(ctx context.Context, root string) (string, error) {
	status, err := r.command(ctx, root, false, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return "", err
	}
	if _, err := bounded(status, r.MaxReadBytes); err != nil {
		return "", err
	}
	fileHashes, err := r.WorktreeFileHashes(ctx, root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write(status)
	paths := make([]string, 0, len(fileHashes))
	for path := range fileHashes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		_, _ = io.WriteString(h, path+"\x00"+fileHashes[path]+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WorktreeFileHashes returns content-and-mode identities for every tracked or
// non-ignored untracked path. Missing tracked paths are retained as "missing"
// entries so deletions participate in delta calculation.
func (r Runner) WorktreeFileHashes(ctx context.Context, root string) (map[string]string, error) {
	pathsRaw, err := r.command(ctx, root, false, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	if _, err := bounded(pathsRaw, r.MaxReadBytes); err != nil {
		return nil, err
	}
	paths := strings.Split(strings.TrimSuffix(string(pathsRaw), "\x00"), "\x00")
	hashes := make(map[string]string, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := model.ValidateRelativePath(path); err != nil {
			return nil, err
		}
		value, err := hashWorktreeFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", path, err)
		}
		hashes[path] = value
	}
	return hashes, nil
}

func hashWorktreeFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = io.WriteString(h, info.Mode().String()+"\x00")
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, target)
	} else {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
