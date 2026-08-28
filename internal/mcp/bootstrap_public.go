package mcp

import (
	"context"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

func bootstrapInputSchema() map[string]any {
	return obj(map[string]any{})
}

func (s *Server) bootstrapPublic(ctx context.Context, raw []byte) (any, error) {
	identity := controller.Controller{Config: s.Service.Config, ConfigPath: s.Service.ConfigPath}.RuntimeIdentity(ctx)
	resolution, err := s.Service.EffectiveProjectSnapshot()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resolution.Projects))
	for id := range resolution.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if max := s.Service.Config.MaxListItems; max > 0 && len(ids) > max {
		ids = ids[:max]
	}
	projects := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		project := resolution.Projects[id]
		code := project.ProjectCode
		if code == "" {
			identifiers, readErr := s.Service.ProjectIdentifiersRead(ctx, id)
			if readErr != nil {
				return nil, readErr
			}
			code = identifiers.ProjectCode
		}
		projects = append(projects, map[string]any{"project_code": code, "project_id": id})
	}
	rules := globalWorkflowRules()
	rules["digest"] = globalWorkflowDigest()
	rules["guidance"] = "Use session_start with a role, bind the returned session to a project, then read project/status before project work."
	return map[string]any{
		"runtime": map[string]any{
			"gateway_ready":      identity.GatewayReady,
			"tunnel_ready":       identity.TunnelReady,
			"version_match":      identity.VersionMatch,
			"exact_source_match": identity.ExactSourceMatch,
			"source_sha":         identity.SourceSHA,
			"running_version":    identity.RunningVersion,
		},
		"projects": projects,
		"rules":    rules,
	}, nil
}
