package gates

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const MaxTokens = 3000

type TokenFile struct {
	Path   string
	Tokens int
}

type TokenReport struct {
	Files []TokenFile
	Max   TokenFile
}

type Command func(context.Context, string, string, ...string) (int, string, error)
type TokenCounter func(context.Context, string) (TokenReport, error)

type Executor struct {
	Command Command
	Tokens  TokenCounter
}

func NewExecutor() Executor {
	return Executor{
		Command: fixedCommand,
		Tokens:  CountTokens,
	}
}

func Resolve(gates []string) ([]string, error) {
	if err := model.ValidateWorkflowGates(gates); err != nil {
		return nil, err
	}
	return model.EffectiveWorkflowGates(gates), nil
}

func (e Executor) Execute(ctx context.Context, root string, requested []string) ([]model.CompletionGateResult, error) {
	resolved, err := Resolve(requested)
	if err != nil {
		return nil, err
	}
	if e.Command == nil || e.Tokens == nil {
		return nil, fmt.Errorf("gate executor is not configured")
	}
	results := make([]model.CompletionGateResult, 0, len(resolved))
	for _, gate := range resolved {
		code, output, runErr := e.runGate(ctx, root, gate)
		results = append(results, model.CompletionGateResult{ID: gate, ExitCode: code})
		if runErr != nil || code != 0 {
			if runErr != nil {
				return results, fmt.Errorf("gate %s failed: %w", gate, runErr)
			}
			return results, fmt.Errorf("gate %s failed with exit code %d: %s", gate, code, compact(output))
		}
	}
	return results, nil
}

func (e Executor) runGate(ctx context.Context, root, gate string) (int, string, error) {
	switch gate {
	case model.WorkflowGateFormat:
		return e.Command(ctx, root, "go", "run", "./cmd/gofmt-struct", "--check", ".")
	case model.WorkflowGateCheck:
		tokens, err := e.Tokens(ctx, root)
		if err != nil {
			return 1, "", fmt.Errorf("mandatory token admission: %w", err)
		}
		if tokens.Max.Tokens > MaxTokens {
			return 1, "", fmt.Errorf("mandatory token admission failed: %s has %d tokens (maximum %d)", tokens.Max.Path, tokens.Max.Tokens, MaxTokens)
		}
		return e.Command(ctx, root, "python3", "scripts/static-check.py")
	case model.WorkflowGateTest:
		return e.Command(ctx, root, "go", "test", "./...", "-count=1")
	default:
		return 1, "", fmt.Errorf("unsupported workflow gate %q", gate)
	}
}

func Format(ctx context.Context, root string) error {
	code, output, err := fixedCommand(ctx, root, "go", "run", "./cmd/gofmt-struct", "--write", ".")
	if err != nil || code != 0 {
		if err != nil {
			return fmt.Errorf("format failed: %w", err)
		}
		return fmt.Errorf("format failed with exit code %d: %s", code, compact(output))
	}
	return nil
}

func CountTokens(ctx context.Context, root string) (TokenReport, error) {
	if root == "" {
		return TokenReport{}, fmt.Errorf("repository root is required")
	}
	files, err := trackedWorkspaceFiles(ctx, root)
	if err != nil {
		return TokenReport{}, err
	}
	oracleRoot, err := os.MkdirTemp("", "gpt-tunnel-token-oracle-")
	if err != nil {
		return TokenReport{}, fmt.Errorf("create token admission workspace: %w", err)
	}
	defer os.RemoveAll(oracleRoot)
	if err := copyWorkspaceFiles(root, oracleRoot, files); err != nil {
		return TokenReport{}, err
	}
	for _, command := range []struct {
		name string
		args []string
	}{
		{name: "git", args: []string{"init", "--quiet"}},
		{name: "git", args: []string{"add", "--all"}},
	} {
		code, output, runErr := fixedCommand(ctx, oracleRoot, command.name, command.args...)
		if runErr != nil || code != 0 {
			return TokenReport{}, fmt.Errorf("token admission workspace setup failed: %s", compact(output))
		}
	}
	if code, output, runErr := fixedCommand(ctx, oracleRoot, "repodex", "init", "--force"); runErr != nil || code != 0 {
		return TokenReport{}, fmt.Errorf("token admission oracle initialization failed: %s", compact(output))
	}
	code, output, stderr, err := fixedCommandStdout(ctx, oracleRoot, "reposuite", "refactor")
	if err != nil || code != 0 {
		if err != nil {
			return TokenReport{}, fmt.Errorf("token admission oracle failed: %w: %s", err, compact(output+stderr))
		}
		return TokenReport{}, fmt.Errorf("token admission oracle failed with exit code %d: %s", code, compact(output+stderr))
	}
	if err := parseRefactorOutput(output); err != nil {
		return TokenReport{}, err
	}
	return TokenReport{}, nil
}

func trackedWorkspaceFiles(ctx context.Context, root string) ([]string, error) {
	code, output, stderr, err := fixedCommandStdout(ctx, root, "git", "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil || code != 0 {
		return nil, fmt.Errorf("list token admission files: %s", compact(output+stderr))
	}
	var files []string
	for _, path := range strings.Split(strings.TrimSpace(output), "\n") {
		if path != "" {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("token admission workspace has no source files")
	}
	return files, nil
}

func copyWorkspaceFiles(root, destination string, files []string) error {
	for _, relative := range files {
		if filepath.IsAbs(relative) || relative == "." || strings.HasPrefix(filepath.Clean(relative), ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe token admission path %q", relative)
		}
		sourcePath := filepath.Join(root, relative)
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return fmt.Errorf("stat token admission file %q: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("token admission file %q is not regular", relative)
		}
		destinationPath := filepath.Join(destination, relative)
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return fmt.Errorf("create token admission path %q: %w", relative, err)
		}
		in, err := os.Open(sourcePath)
		if err != nil {
			return fmt.Errorf("open token admission file %q: %w", relative, err)
		}
		out, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return fmt.Errorf("create token admission file %q: %w", relative, err)
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		_ = in.Close()
		if copyErr != nil {
			return fmt.Errorf("copy token admission file %q: %w", relative, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close token admission file %q: %w", relative, closeErr)
		}
	}
	return nil
}

func parseRefactorOutput(output string) error {
	fields := strings.Fields(output)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "threshold=") || !strings.HasPrefix(fields[1], "files=") {
		return fmt.Errorf("token admission oracle output is malformed")
	}
	threshold, err := strconv.Atoi(strings.TrimPrefix(fields[0], "threshold="))
	if err != nil || threshold != MaxTokens {
		return fmt.Errorf("token admission oracle threshold is not %d", MaxTokens)
	}
	files, err := strconv.Atoi(strings.TrimPrefix(fields[1], "files="))
	if err != nil || files < 0 {
		return fmt.Errorf("token admission oracle file count is malformed")
	}
	if files != 0 {
		return fmt.Errorf("mandatory token admission failed: %s", compact(strings.TrimSpace(output)))
	}
	return nil
}

func fixedCommand(ctx context.Context, dir string, name string, args ...string) (int, string, error) {
	return fixedCommandWithDir(ctx, dir, name, args...)
}

func fixedCommandWithDir(ctx context.Context, dir, name string, args ...string) (int, string, error) {
	code, stdout, stderr, err := fixedCommandStdout(ctx, dir, name, args...)
	return code, stdout + stderr, err
}

func fixedCommandStdout(ctx context.Context, dir, name string, args ...string) (int, string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if name == "repodex" && len(args) >= 2 && args[0] == "init" && args[1] == "--force" {
		cmd.Stdin = strings.NewReader("\n")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			code = 1
		}
	}
	if err != nil {
		return code, stdout.String(), stderr.String(), err
	}
	return code, stdout.String(), stderr.String(), nil
}

func compact(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
