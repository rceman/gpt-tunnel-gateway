package mcp

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func addMCP7BootstrapTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server) {
	add("status", "Return gateway health and runtime identity without a durable session.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		runtime := controller.Controller{Config: s.Service.Config, ConfigPath: s.Service.ConfigPath}.RuntimeIdentity(ctx)
		version := runtime.RunningVersion
		if version == "" {
			version = runtime.InstalledVersion
		}
		projects, projectsOK := mcpRegisteredProjects(s.Service)
		state, nextAction := mcpStatusProjection(runtime)
		if !projectsOK && state == "ready" {
			state = "degraded"
			nextAction = "Repair the local project registry before using session_start."
		}
		return map[string]any{
			"service":                 "gpt-tunnel-gatewayd",
			"version":                 version,
			"gateway_id":              s.Service.Config.GatewayID,
			"status":                  state,
			"recommended_next_action": nextAction,
			"registered_projects":     projects,
			"runtime_identity":        runtime,
		}, nil
	})
}

const mcpStatusMaxProjects = 100

func mcpRegisteredProjects(s *service.Service) ([]map[string]any, bool) {
	resolution, err := s.EffectiveProjectSnapshot()
	if err != nil {
		return []map[string]any{}, false
	}
	ids := make([]string, 0, len(resolution.Projects))
	for id := range resolution.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > mcpStatusMaxProjects {
		ids = ids[:mcpStatusMaxProjects]
	}
	projects := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		project := resolution.Projects[id]
		projects = append(projects, map[string]any{"project_id": id, "project_code": project.ProjectCode})
	}
	return projects, true
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
