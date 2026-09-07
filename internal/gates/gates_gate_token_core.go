package gates

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

func (e Executor) runGate(ctx context.Context, root, gate string, scope TestScope) (int, string, error) {
	switch gate {
	case model.WorkflowGateFormat:
		return e.Command(ctx, root, "go", "run", "./cmd/gofmt-struct", "--check", ".")
	case model.WorkflowGateCheck:
		tokens, err := e.Tokens(ctx, root)
		if err != nil {
			return 1, "", fmt.Errorf("mandatory token admission: %w", err)
		}
		if len(tokens.Offending) > 0 || tokens.Max.Tokens > MaxTokens {
			offenders := tokens.Offending
			if len(offenders) == 0 {
				offenders = []TokenFile{tokens.Max}
			}
			return 1, "", fmt.Errorf("mandatory token admission failed: %s", formatOffenders(offenders))
		}
		return e.Command(ctx, root, "python3", "scripts/static-check.py")
	case model.WorkflowGateTest:
		args, err := scope.CommandArgs()
		if err != nil {
			return 1, "", err
		}
		if len(args) == 0 {
			return 0, "no affected Go packages", nil
		}
		return e.Command(ctx, root, args[0], args[1:]...)
	default:
		return 1, "", fmt.Errorf("unsupported workflow gate %q", gate)
	}
}

func Format(ctx context.Context, root string) error {
	return runFormat(ctx, root, "--write", ".")
}

func CheckFormat(ctx context.Context, root string) error {
	return runFormat(ctx, root, "--check", ".")
}

func CheckFormatFiles(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	validated := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if err := model.ValidateRelativePath(path); err != nil {
			return err
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		validated = append(validated, path)
	}
	sort.Strings(validated)
	return runFormat(ctx, root, "--check", validated...)
}

func runFormat(ctx context.Context, root, mode string, paths ...string) error {
	args := append([]string{"go", "run", "./cmd/gofmt-struct", mode}, paths...)
	code, output, err := fixedCommand(ctx, root, args[0], args[1:]...)
	if err != nil || code != 0 {
		if err != nil {
			if detail := compact(output); detail != "" {
				return fmt.Errorf("format failed: %w: %s", err, detail)
			}
			return fmt.Errorf("format failed: %w", err)
		}
		return fmt.Errorf("format failed with exit code %d: %s", code, compact(output))
	}
	return nil
}

func CountTokens(ctx context.Context, root string) (TokenReport, error) {
	return tokenizer.CountRepository(ctx, root, tokenizer.NewCounter())
}

func formatOffenders(files []TokenFile) string {
	const maxShown = 8
	if len(files) > maxShown {
		files = files[:maxShown]
	}
	parts := make([]string, 0, len(files))
	for _, file := range files {
		parts = append(parts, fmt.Sprintf("%s=%d", file.Path, file.Tokens))
	}
	return strings.Join(parts, ", ")
}

func fixedCommand(ctx context.Context, dir string, name string, args ...string) (int, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var output tailOutputBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			code = 1
		}
	}
	return code, output.String(), err
}

type tailOutputBuffer struct {
	data      []byte
	truncated bool
}

func (b *tailOutputBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) >= maxGateOutputBytes {
		hadData := len(b.data) > 0
		b.data = append(b.data[:0], p[len(p)-maxGateOutputBytes:]...)
		b.truncated = len(p) > maxGateOutputBytes || hadData || b.truncated
		return len(p), nil
	}
	if len(b.data)+len(p) > maxGateOutputBytes {
		drop := len(b.data) + len(p) - maxGateOutputBytes
		b.data = append(b.data[drop:], p...)
		b.truncated = true
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *tailOutputBuffer) String() string {
	if !b.truncated {
		return string(b.data)
	}
	maxTail := maxGateOutputBytes - len(gateOutputTruncationMarker)
	tail := b.data
	if len(tail) > maxTail {
		tail = tail[len(tail)-maxTail:]
	}
	return gateOutputTruncationMarker + string(tail)
}

func compact(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
