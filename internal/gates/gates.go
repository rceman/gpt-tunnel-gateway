package gates

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

const MaxTokens = tokenizer.MaxTokens

type TokenFile = tokenizer.FileCount
type TokenReport = tokenizer.Report

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
		if len(tokens.Offending) > 0 || tokens.Max.Tokens > MaxTokens {
			offenders := tokens.Offending
			if len(offenders) == 0 {
				offenders = []TokenFile{tokens.Max}
			}
			return 1, "", fmt.Errorf("mandatory token admission failed: %s", formatOffenders(offenders))
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
	return code, stdout.String() + stderr.String(), err
}

func compact(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
