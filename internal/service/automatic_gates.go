package service

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	automaticGateDefaultOutput = int64(1 << 20)
)

type automaticGateTimeoutClass string

const (
	automaticGateTimeoutTight   automaticGateTimeoutClass = "tight"
	automaticGateTimeoutPython  automaticGateTimeoutClass = "python"
	automaticGateTimeoutFocused automaticGateTimeoutClass = "focused"
	automaticGateTimeoutGoVet   automaticGateTimeoutClass = "go_vet"
	automaticGateTimeoutGoTest  automaticGateTimeoutClass = "go_test"
	automaticGateTimeoutGoRace  automaticGateTimeoutClass = "go_race"
)

const (
	automaticGateTightTimeout   = 15 * time.Second
	automaticGatePythonTimeout  = 2 * time.Minute
	automaticGateFocusedTimeout = 2 * time.Minute
	automaticGateGoVetTimeout   = 3 * time.Minute
	automaticGateGoTestTimeout  = 5 * time.Minute
	automaticGateGoRaceTimeout  = 10 * time.Minute
)

type automaticGateDefinition struct {
	argv         []string
	description  string
	timeoutClass automaticGateTimeoutClass
	postCheck    func(stdout, stderr string) error
}

func lookupAutomaticGateDefinition(name string) (automaticGateDefinition, string) {
	name = strings.TrimSpace(name)
	definitions := map[string]automaticGateDefinition{
		"gofmt check": {
			argv:         []string{"gofmt", "-l", "."},
			description:  "gofmt -l .",
			timeoutClass: automaticGateTimeoutTight,
			postCheck: func(stdout, stderr string) error {
				if strings.TrimSpace(stdout) != "" {
					return fmt.Errorf("gofmt reported files requiring formatting")
				}
				return nil
			},
		},
		"test -z \"$(gofmt -l .)\"": {
			argv:         []string{"gofmt", "-l", "."},
			description:  "gofmt -l .",
			timeoutClass: automaticGateTimeoutTight,
			postCheck: func(stdout, stderr string) error {
				if strings.TrimSpace(stdout) != "" {
					return fmt.Errorf("gofmt reported files requiring formatting")
				}
				return nil
			},
		},
		"go vet ./...": {
			argv:         []string{"go", "vet", "./..."},
			description:  "go vet ./...",
			timeoutClass: automaticGateTimeoutGoVet,
		},
		"go test ./...": {
			argv:         []string{"go", "test", "./..."},
			description:  "go test ./...",
			timeoutClass: automaticGateTimeoutGoTest,
		},
		"one bounded go test ./... attempt": {
			argv:         []string{"go", "test", "./..."},
			description:  "go test ./...",
			timeoutClass: automaticGateTimeoutGoTest,
		},
		"go test -race ./...": {
			argv:         []string{"go", "test", "-race", "./..."},
			description:  "go test -race ./...",
			timeoutClass: automaticGateTimeoutGoRace,
		},
		"git diff --check": {
			argv:         []string{"git", "diff", "--check"},
			description:  "git diff --check",
			timeoutClass: automaticGateTimeoutTight,
		},
		"clean pushed branch": {
			argv:         []string{"git", "status", "--porcelain"},
			description:  "git status --porcelain (published branch verified by canonical repository proof)",
			timeoutClass: automaticGateTimeoutTight,
			postCheck: func(stdout, stderr string) error {
				if strings.TrimSpace(stdout) != "" {
					return fmt.Errorf("worktree is not clean")
				}
				return nil
			},
		},
		"python3 scripts/static-check.py": {
			argv:         []string{"python3", "scripts/static-check.py"},
			description:  "python3 scripts/static-check.py",
			timeoutClass: automaticGateTimeoutPython,
		},
		"static checks": {
			argv:         []string{"python3", "scripts/static-check.py"},
			description:  "python3 scripts/static-check.py",
			timeoutClass: automaticGateTimeoutPython,
		},
		"focused automatic gate runner/finalization tests": {
			argv:         []string{"go", "test", "./internal/service", "-run", "Automatic|Finalize"},
			description:  "go test ./internal/service -run Automatic|Finalize",
			timeoutClass: automaticGateTimeoutFocused,
		},
		"focused timeout/failure/override negative tests": {
			argv:         []string{"go", "test", "./internal/service", "-run", "Automatic|Finalize"},
			description:  "go test ./internal/service -run Automatic|Finalize",
			timeoutClass: automaticGateTimeoutFocused,
		},
		"historical completion/report compatibility tests": {
			argv:         []string{"go", "test", "./internal/model", "./internal/service", "-run", "Completion|Report|Historical"},
			description:  "go test ./internal/model ./internal/service -run Completion|Report|Historical",
			timeoutClass: automaticGateTimeoutFocused,
		},
		"MCP/CLI schema parity for canonical finalize/read evidence": {
			argv:         []string{"go", "test", "./internal/mcp", "./cmd/gpt-tunnel", "-run", "Finalize|Report|Completion"},
			description:  "go test ./internal/mcp ./cmd/gpt-tunnel -run Finalize|Report|Completion",
			timeoutClass: automaticGateTimeoutFocused,
		},
	}
	if definition, ok := definitions[name]; ok {
		return definition, ""
	}
	if strings.HasPrefix(strings.ToLower(name), "manual:") || strings.Contains(strings.ToLower(name), "manual") {
		return automaticGateDefinition{}, "manual gate requires bounded external evidence"
	}
	return automaticGateDefinition{}, "unsupported gate definition"
}

type boundedGateOutput struct {
	data      []byte
	limit     int64
	truncated bool
}

func (w *boundedGateOutput) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		w.truncated = len(p) > 0
		return len(p), nil
	}
	remaining := w.limit - int64(len(w.data))
	if remaining <= 0 {
		w.truncated = len(p) > 0
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		w.data = append(w.data, p[:remaining]...)
		w.truncated = true
		return len(p), nil
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

func (w *boundedGateOutput) String() string { return string(w.data) }

var automaticGateExecutor = executeAutomaticGate

func executeAutomaticGate(ctx context.Context, root string, definition automaticGateDefinition, timeout time.Duration, outputLimit int64) (model.CompletionGateResult, error) {
	if runtime.GOOS != "linux" {
		return model.CompletionGateResult{}, fmt.Errorf("automatic gate execution is supported only on Linux")
	}
	if len(definition.argv) == 0 {
		return model.CompletionGateResult{}, fmt.Errorf("automatic gate has no command")
	}
	if timeout <= 0 {
		timeout = automaticGateTightTimeout
	}
	if outputLimit <= 0 {
		outputLimit = automaticGateDefaultOutput
	}
	started := time.Now().UTC()
	command := strings.Join(definition.argv, " ")
	result := model.CompletionGateResult{
		Kind:      "executable",
		Outcome:   "failed",
		Command:   command,
		StartedAt: &started,
	}
	gateCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(gateCtx, definition.argv[0], definition.argv[1:]...)
	cmd.Dir = root
	var stdout, stderr boundedGateOutput
	stdout.limit, stderr.limit = outputLimit, outputLimit
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	finished := time.Now().UTC()
	result.FinishedAt = &finished
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	result.OutputTruncated = stdout.truncated || stderr.truncated
	result.ExitCode = 0
	if err != nil {
		result.ExitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
	}
	if gateCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.Outcome = "timeout"
		result.Evidence = fmt.Sprintf("command exceeded bounded timeout of %s", timeout)
		return result, nil
	}
	if err != nil {
		result.Evidence = "command exited non-zero"
		return result, nil
	}
	if result.OutputTruncated {
		result.Evidence = "command output exceeded the bounded capture limit"
		return result, nil
	}
	if definition.postCheck != nil {
		if checkErr := definition.postCheck(result.Stdout, result.Stderr); checkErr != nil {
			result.Evidence = checkErr.Error()
			return result, nil
		}
	}
	result.Outcome = "passed"
	result.Evidence = "command exited successfully"
	return result, nil
}

func automaticGateTimeoutFor(definition automaticGateDefinition) time.Duration {
	switch definition.timeoutClass {
	case automaticGateTimeoutPython:
		return automaticGatePythonTimeout
	case automaticGateTimeoutFocused:
		return automaticGateFocusedTimeout
	case automaticGateTimeoutGoVet:
		return automaticGateGoVetTimeout
	case automaticGateTimeoutGoTest:
		return automaticGateGoTestTimeout
	case automaticGateTimeoutGoRace:
		return automaticGateGoRaceTimeout
	case automaticGateTimeoutTight:
		fallthrough
	default:
		return automaticGateTightTimeout
	}
}

func (s *Service) runAutomaticGates(ctx context.Context, task model.Task, projectRoot string) ([]model.CompletionGateResult, string, error) {
	results := make([]model.CompletionGateResult, 0, len(task.RequiredGates))
	status := "succeeded"
	for i, name := range task.RequiredGates {
		id := fmt.Sprintf("G%d", i+1)
		definition, reason := lookupAutomaticGateDefinition(name)
		if reason != "" {
			kind := "unsupported"
			outcome := "unsupported"
			if strings.Contains(strings.ToLower(name), "manual") {
				kind, outcome = "manual", "manual"
			}
			results = append(results, model.CompletionGateResult{ID: id, ExitCode: -1, Kind: kind, Outcome: outcome, Evidence: reason})
			if status == "succeeded" {
				status = "needs_gpt_revision"
			}
			continue
		}
		result, err := automaticGateExecutor(ctx, projectRoot, definition, automaticGateTimeoutFor(definition), s.Config.MaxReadBytes)
		if err != nil {
			return nil, "", fmt.Errorf("execute %s: %w", id, err)
		}
		result.ID = id
		results = append(results, result)
		if result.Outcome != "passed" {
			status = "failed"
		}
	}
	return results, status, nil
}
