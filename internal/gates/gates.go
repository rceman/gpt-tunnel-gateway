package gates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
)

const MaxTokens = tokenizer.MaxTokens

const maxGateOutputBytes = 64 << 10

const gateOutputTruncationMarker = "[gate output truncated; showing final output bytes]\n"

const GateOptimizationWarning = "GATES_OPTIMIZATION_REQUIRED"

const gateOptimizationBudget = 30 * time.Second

const TestGateRunnerContractVersion = "gpt-tunnel-test-gate/v2"

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
	started := time.Now()
	for _, gate := range resolved {
		gateStarted := time.Now()
		code, output, runErr := e.runGate(ctx, root, gate, normalized)
		results = append(results, timedGateResult(gate, code, time.Since(gateStarted)))
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
	annotateGateAggregate(results, time.Since(started))
	return results, nil
}

// ExecuteWithProjectCommands runs the exact gate groups selected by policy
// using the repository-owned argv definitions. The Gateway only selects the
// named group and test mode; it does not derive language-specific package
// scopes or rewrite command arguments.
func (e Executor) ExecuteWithProjectCommands(ctx context.Context, root string, requested []string, commands model.ProjectGateCommands, testMode string) ([]model.CompletionGateResult, error) {
	return e.ExecuteWithProjectCommandsAndScope(ctx, root, requested, commands, testMode, FullTestScope())
}

// ExecuteWithProjectCommandsAndScope preserves project-owned format/check
// commands while applying the server-owned affected-package scope to the
// canonical Go task test command. Unknown/custom test commands fail closed to
// the configured full command; the scope resolver itself already falls back
// to FullTestScope on uncertainty.
func (e Executor) ExecuteWithProjectCommandsAndScope(ctx context.Context, root string, requested []string, commands model.ProjectGateCommands, testMode string, scope TestScope) ([]model.CompletionGateResult, error) {
	resolved, err := Resolve(requested)
	if err != nil {
		return nil, err
	}
	if err := commands.Validate(); err != nil {
		return nil, err
	}
	if testMode != "task" && testMode != "train" {
		return nil, fmt.Errorf("invalid project test mode %q", testMode)
	}
	if e.Command == nil {
		return nil, fmt.Errorf("gate executor is not configured")
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return nil, err
	}
	results := make([]model.CompletionGateResult, 0, len(resolved))
	started := time.Now()
	for _, gate := range resolved {
		gateStarted := time.Now()
		argv, err := ProjectGateCommandArgs(commands, gate, testMode, normalized)
		if err != nil {
			return results, err
		}
		code, output, runErr := e.Command(ctx, root, argv[0], argv[1:]...)
		results = append(results, timedGateResult(gate, code, time.Since(gateStarted)))
		if runErr != nil || code != 0 {
			return results, gateFailure(gate, code, output, runErr)
		}
	}
	annotateGateAggregate(results, time.Since(started))
	return results, nil
}

// ProjectGateCommandArgs returns the exact argv that a project-owned gate
// would execute after applying the server-owned scope to a canonical Go task
// test command.
func ProjectGateCommandArgs(commands model.ProjectGateCommands, gate, testMode string, scope TestScope) ([]string, error) {
	if err := commands.Validate(); err != nil {
		return nil, err
	}
	if testMode != "task" && testMode != "train" {
		return nil, fmt.Errorf("invalid project test mode %q", testMode)
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return nil, err
	}
	command := commands.Format
	switch gate {
	case model.WorkflowGateCheck:
		command = commands.Check
	case model.WorkflowGateTest:
		if testMode == "task" {
			command = commands.Test.Task
		} else {
			command = commands.Test.Train
		}
	case model.WorkflowGateFormat:
	default:
		return nil, fmt.Errorf("unsupported workflow gate %q", gate)
	}
	argv := append([]string{}, command.Command...)
	if gate == model.WorkflowGateTest && testMode == "task" && normalized.Mode == TestScopePackages {
		argv = scopedGoTestCommand(argv, normalized)
	}
	return argv, nil
}

func ProjectGateCommandDigest(commands model.ProjectGateCommands, gate, testMode string, scope TestScope) (string, error) {
	argv, err := ProjectGateCommandArgs(commands, gate, testMode, scope)
	if err != nil {
		return "", err
	}
	normalized, err := scope.Normalize()
	if err != nil {
		return "", err
	}
	scopeIdentity := gatesScopeIdentity(normalized, gate)
	payload := struct {
		Version string   `json:"version"`
		Gate    string   `json:"gate"`
		Scope   string   `json:"scope"`
		Argv    []string `json:"argv"`
	}{"gpt-tunnel-project-gate/v1", gate, scopeIdentity, argv}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func gatesScopeIdentity(scope TestScope, gate string) string {
	if gate != model.WorkflowGateTest {
		return TestScopeFull
	}
	if scope.Mode == TestScopePackages {
		identity, err := scope.CommandIdentity()
		if err == nil {
			return identity
		}
	}
	return scope.Mode
}

func scopedGoTestCommand(command []string, scope TestScope) []string {
	if len(command) < 2 || command[0] != "go" || command[1] != "test" {
		return command
	}
	args, err := scope.CommandArgs()
	if err != nil || len(args) < 2 {
		return command
	}
	result := []string{"go", "test"}
	hasCount := false
	for _, arg := range command[2:] {
		if arg == "./..." {
			result = append(result, args[2:len(args)-1]...)
			continue
		}
		if strings.HasPrefix(arg, "-") {
			result = append(result, arg)
			if strings.HasPrefix(arg, "-count=") {
				hasCount = true
			}
		}
	}
	if len(result) == 2 {
		return command
	}
	if !hasCount {
		result = append(result, "-count=1")
	}
	return result
}

func timedGateResult(id string, exitCode int, elapsed time.Duration) model.CompletionGateResult {
	result := model.CompletionGateResult{ID: id, ExitCode: exitCode, DurationMS: elapsed.Milliseconds()}
	if elapsed >= gateOptimizationBudget {
		result.Warnings = []string{fmt.Sprintf("%s: gate=%s duration_ms=%d", GateOptimizationWarning, id, result.DurationMS)}
	}
	return result
}

func annotateGateAggregate(results []model.CompletionGateResult, elapsed time.Duration) {
	if len(results) == 0 {
		return
	}
	ms := elapsed.Milliseconds()
	results[0].AggregateMS = ms
	if elapsed >= gateOptimizationBudget {
		results[0].Warnings = append(results[0].Warnings, fmt.Sprintf("%s: aggregate_ms=%d", GateOptimizationWarning, ms))
	}
}

func gateFailure(gate string, code int, output string, runErr error) error {
	detail := strings.TrimSpace(output)
	if runErr != nil {
		if detail != "" {
			return fmt.Errorf("gate %s failed: %w:\n%s", gate, runErr, detail)
		}
		return fmt.Errorf("gate %s failed: %w", gate, runErr)
	}
	if detail != "" {
		return fmt.Errorf("gate %s failed with exit code %d:\n%s", gate, code, detail)
	}
	return fmt.Errorf("gate %s failed with exit code %d", gate, code)
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
