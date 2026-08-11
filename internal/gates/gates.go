package gates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

const MaxTokens = tokenizer.MaxTokens

const maxGateOutputBytes = 64 << 10

const gateOutputTruncationMarker = "[gate output truncated; showing final output bytes]\n"

const TestGateRunnerContractVersion = "gpt-tunnel-test-gate/v1"

type TokenFile = tokenizer.FileCount
type TokenReport = tokenizer.Report

func TestGateContractDigest(gates []string) (string, error) {
	resolved, err := Resolve(gates)
	if err != nil {
		return "", err
	}
	payload := struct {
		Version string   `json:"version"`
		Gates   []string `json:"gates"`
	}{
		Version: TestGateRunnerContractVersion,
		Gates:   resolved,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func TestGateCommandContractDigest(gates []string, scope TestScope) (string, error) {
	resolved, err := Resolve(gates)
	if err != nil {
		return "", err
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return "", err
	}
	if normalized.Mode == TestScopeFull {
		return TestGateContractDigest(resolved)
	}
	identity, err := normalized.CommandIdentity()
	if err != nil {
		return "", err
	}
	payload := struct {
		Version   string   `json:"version"`
		Gates     []string `json:"gates"`
		TestScope string   `json:"test_scope"`
	}{
		Version:   TestGateRunnerContractVersion,
		Gates:     resolved,
		TestScope: identity,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
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
	return e.ExecuteWithScope(ctx, root, requested, FullTestScope())
}

func (e Executor) ExecuteWithScope(ctx context.Context, root string, requested []string, scope TestScope) ([]model.CompletionGateResult, error) {
	resolved, err := Resolve(requested)
	if err != nil {
		return nil, err
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return nil, err
	}
	if e.Command == nil || e.Tokens == nil {
		return nil, fmt.Errorf("gate executor is not configured")
	}
	results := make([]model.CompletionGateResult, 0, len(resolved))
	for _, gate := range resolved {
		code, output, runErr := e.runGate(ctx, root, gate, normalized)
		results = append(results, model.CompletionGateResult{ID: gate, ExitCode: code})
		if runErr != nil || code != 0 {
			detail := strings.TrimSpace(output)
			if detail != "" {
				if runErr != nil {
					return results, fmt.Errorf("gate %s failed: %w:\n%s", gate, runErr, detail)
				}
				return results, fmt.Errorf("gate %s failed with exit code %d:\n%s", gate, code, detail)
			}
			if runErr != nil {
				return results, fmt.Errorf("gate %s failed: %w", gate, runErr)
			}
			return results, fmt.Errorf("gate %s failed with exit code %d", gate, code)
		}
	}
	return results, nil
}

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
