package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func runIntegrationHook(ctx context.Context, command model.ProjectGateCommand, root string) (string, error) {
	if len(command.Command) == 0 {
		return "not_configured", nil
	}
	if err := command.Validate("integration hook"); err != nil {
		return "", err
	}
	process := exec.CommandContext(ctx, command.Command[0], command.Command[1:]...)
	process.Dir = root
	var output boundedHookOutput
	process.Stdout = &output
	process.Stderr = &output
	if err := process.Run(); err != nil {
		return "", fmt.Errorf("integration hook %q failed: %w: %s", command.Command[0], err, strings.TrimSpace(output.String()))
	}
	return "completed", nil
}

type boundedHookOutput struct{ data []byte }

func (b *boundedHookOutput) Write(p []byte) (int, error) {
	const maxBytes = 16 << 10
	remaining := maxBytes - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return len(p), nil
}

func (b *boundedHookOutput) String() string { return string(b.data) }
