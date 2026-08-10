package tokenizer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pkoukk/tiktoken-go"
)

const (
	EncodingName = "o200k_base"
	MaxTokens    = 3000
)

// Counter owns one lazily initialized tokenizer for its lifetime.
type Counter struct {
	once sync.Once
	enc  *tiktoken.Tiktoken
	err  error
}

func NewCounter() *Counter { return &Counter{} }

func (c *Counter) encoding() (*tiktoken.Tiktoken, error) {
	c.once.Do(func() {
		c.enc, c.err = tiktoken.GetEncoding(EncodingName)
	})
	if c.err != nil {
		return nil, fmt.Errorf("initialize %s tokenizer: %w", EncodingName, c.err)
	}
	return c.enc, nil
}

func (c *Counter) CountText(data []byte) (int, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return 0, fmt.Errorf("token admission input contains NUL")
	}
	if !utf8.Valid(data) {
		return 0, fmt.Errorf("token admission input is not valid UTF-8")
	}
	enc, err := c.encoding()
	if err != nil {
		return 0, err
	}
	return len(enc.Encode(string(data), nil, nil)), nil
}

type FileCount struct {
	Path   string
	Tokens int
}

type Report struct {
	Files         []FileCount
	Offending     []FileCount
	Max           FileCount
	CountAboveMax int
}

func CountRepository(ctx context.Context, root string, counter *Counter) (Report, error) {
	if root == "" {
		return Report{}, fmt.Errorf("repository root is required")
	}
	if counter == nil {
		counter = NewCounter()
	}
	paths, err := repositoryFiles(ctx, root)
	if err != nil {
		return Report{}, err
	}
	if len(paths) == 0 {
		return Report{}, fmt.Errorf("repository has no applicable source files")
	}
	report := Report{Files: make([]FileCount, 0, len(paths))}
	for _, path := range paths {
		if !Applicable(path) {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Lstat(full)
		if err != nil {
			return Report{}, fmt.Errorf("stat applicable file %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return Report{}, fmt.Errorf("applicable file %q is not regular", path)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return Report{}, fmt.Errorf("read applicable file %q: %w", path, err)
		}
		tokens, err := counter.CountText(data)
		if err != nil {
			return Report{}, fmt.Errorf("count applicable file %q: %w", path, err)
		}
		file := FileCount{
			Path:   path,
			Tokens: tokens,
		}
		report.Files = append(report.Files, file)
		if tokens > report.Max.Tokens || report.Max.Path == "" {
			report.Max = file
		}
		if tokens > MaxTokens {
			report.CountAboveMax++
			report.Offending = append(report.Offending, file)
		}
	}
	if len(report.Files) == 0 {
		return Report{}, fmt.Errorf("repository has no applicable source files")
	}
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path })
	sort.Slice(report.Offending, func(i, j int) bool {
		if report.Offending[i].Tokens != report.Offending[j].Tokens {
			return report.Offending[i].Tokens > report.Offending[j].Tokens
		}
		return report.Offending[i].Path < report.Offending[j].Path
	})
	return report, nil
}

func repositoryFiles(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list repository files: %w", err)
	}
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		path := filepath.ToSlash(string(part))
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) || path == "." || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
			return nil, fmt.Errorf("unsafe repository path %q", path)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// Applicable is the single repository-wide source/test/tooling eligibility rule.
func Applicable(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || strings.HasPrefix(path, "../") {
		return false
	}
	for _, excluded := range []string{".git/", ".repodex/", "dist/", "vendor/", "__pycache__/"} {
		if strings.HasPrefix(path, excluded) || strings.Contains(path, "/"+excluded) {
			return false
		}
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".ico" || ext == ".pdf" || ext == ".zip" || ext == ".gz" || ext == ".woff" || ext == ".woff2" {
		return false
	}
	if strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "scripts/") || strings.HasPrefix(path, "tests/") || strings.HasPrefix(path, "schemas/") {
		return ext == ".go" || ext == ".py" || ext == ".sh" || ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".md" || ext == ".txt" || ext == ".lock" || ext == ".mod" || ext == ".sum"
	}
	return ext == ".go" || ext == ".py" || ext == ".sh" || ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".md" || ext == ".txt" || ext == ".lock" || ext == ".mod" || ext == ".sum" || path == "VERSION" || path == "CHANGELOG"
}
