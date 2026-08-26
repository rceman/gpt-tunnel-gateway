package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

func addMCP7BootstrapTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server) {
	add("status", "Return gateway health and runtime identity without a durable session.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		runtime := controller.Controller{Config: s.Service.Config, ConfigPath: s.Service.ConfigPath}.RuntimeIdentity(ctx)
		version := runtime.RunningVersion
		if version == "" {
			version = runtime.InstalledVersion
		}
		state, nextAction := mcpStatusProjection(runtime)
		return map[string]any{
			"service":                 "gpt-tunnel-gatewayd",
			"version":                 version,
			"gateway_id":              s.Service.Config.GatewayID,
			"time":                    time.Now().UTC(),
			"status":                  state,
			"recommended_next_action": nextAction,
			"runtime_identity":        runtime,
		}, nil
	})
}

func mcpStatusProjection(runtime controller.RuntimeIdentity) (string, string) {
	switch {
	case !runtime.GatewayReady:
		return "unavailable", "Restore Gateway readiness before using session_start, call, or batch."
	case !runtime.VersionMatch:
		return "degraded", "Verify Gateway artifact identity before using control-plane actions."
	case !runtime.TunnelReady:
		return "degraded", "Restore Tunnel readiness before remote control-plane use."
	default:
		return "ready", "Call session_start with project_id to create a project-bound Planner session."
	}
}
