package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

// activateTaskSource is the legacy service adapter. The reusable activation
// implementation belongs to internal/activation so Train v2 can use the same
// exact-source transaction without importing service internals.
func activateTaskSource(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (TaskActivationResult, error) {
	result, err := activation.Source(ctx, c, configPath, project, sourceHead)
	if err != nil {
		return TaskActivationResult{}, err
	}
	return TaskActivationResult{SourceHead: result.SourceHead, Activation: result.Activation, Smoke: result.Smoke, TunnelPID: result.TunnelPID, GatewayPID: result.GatewayPID}, nil
}

func boundedTaskOutput(data []byte) string {
	return activation.BoundedOutput(data)
}
