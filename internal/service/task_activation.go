package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

// activateTaskSource is the server-owned GTW self-activation adapter. The
// Gateway handoff is never exposed as a generic public activation action.
func activateTaskSource(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (TaskActivationResult, error) {
	result, err := activation.SelfActivate(ctx, c, configPath, project, sourceHead)
	if err != nil {
		return TaskActivationResult{}, err
	}
	return TaskActivationResult{
		SourceHead: result.SourceHead,
		Activation: result.Activation,
		Smoke:      result.Smoke,
		TunnelPID:  result.TunnelPID,
		GatewayPID: result.GatewayPID,
	}, nil
}

func boundedTaskOutput(data []byte) string {
	return activation.BoundedOutput(data)
}
