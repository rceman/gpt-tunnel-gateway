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
		return map[string]any{
			"service":          "gpt-tunnel-gatewayd",
			"version":          version,
			"gateway_id":       s.Service.Config.GatewayID,
			"time":             time.Now().UTC(),
			"runtime_identity": runtime,
		}, nil
	})
}
