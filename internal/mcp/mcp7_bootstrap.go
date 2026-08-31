package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

func addMCP7BootstrapTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server) {
	add("status", "Return bounded Gateway readiness and identity without a durable session.", statusPublicInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.statusPublic(ctx)
	})
	add("guide", "Describe the bounded GPT Tunnel bootstrap sequence and server-authorized roles.", guidePublicInputSchema(), func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{
			"roles": []map[string]any{
				{"key": "planner", "ref_required": false},
				{"key": "agent", "ref_required": true, "ref_semantics": "Exact Airelay session key."},
			},
			"steps": []string{
				"Use status to confirm Gateway ingress and readiness.",
				"Use projects with the Gateway key to discover registered projects.",
				"Start a session with Gateway, project, and role.",
				"Use schema with the bound session to discover an action contract.",
				"Use call with the same session to execute a server-owned action.",
			},
		}, nil
	})
	add("projects", "List locally registered projects for one Gateway without a durable session.", projectsPublicInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.projectsPublic(ctx, raw)
	})
}

func statusPublicInputSchema() map[string]any {
	return emptyPublicInputSchema()
}

func guidePublicInputSchema() map[string]any {
	return emptyPublicInputSchema()
}

func emptyPublicInputSchema() map[string]any {
	schema := obj(map[string]any{})
	schema["required"] = []string{}
	return schema
}

func projectsPublicInputSchema() map[string]any {
	gateway := str("Canonical registered Gateway key.")
	gateway["minLength"] = 1
	return obj(map[string]any{
		"gateway": gateway,
	}, "gateway")
}

func statusPublicOutputSchema() map[string]any {
	lastError := closedOutput(map[string]any{
		"code":    outputString(),
		"message": outputString(),
	}, "code", "message")
	gateway := closedOutput(map[string]any{
		"key":        outputString(),
		"ready":      outputBoolean(),
		"label":      outputString(),
		"last_seen":  outputDateTime(),
		"last_error": lastError,
	}, "key", "ready")
	return closedOutput(map[string]any{
		"ready":       outputBoolean(),
		"gateways":    outputArray(gateway),
		"captured_at": outputDateTime(),
	}, "ready", "gateways", "captured_at")
}

func guidePublicOutputSchema() map[string]any {
	role := closedOutput(map[string]any{
		"key":           outputString(),
		"ref_required":  outputBoolean(),
		"ref_semantics": outputString(),
	}, "key", "ref_required")
	return closedOutput(map[string]any{
		"roles": outputArray(role),
		"steps": outputArray(outputString()),
	}, "roles", "steps")
}

func projectsPublicOutputSchema() map[string]any {
	project := closedOutput(map[string]any{
		"key":  outputString(),
		"name": outputString(),
	}, "key", "name")
	gateway := closedOutput(map[string]any{
		"key":   outputString(),
		"label": outputString(),
	}, "key")
	return closedOutput(map[string]any{
		"gateway":  gateway,
		"projects": outputArray(project),
	}, "gateway", "projects")
}

func (s *Server) statusPublic(ctx context.Context) (any, error) {
	runtime := controller.Controller{Config: s.Service.Config, ConfigPath: s.Service.ConfigPath}.RuntimeIdentity(ctx)
	ready := runtime.GatewayReady
	item := map[string]any{
		"key":   s.Service.Config.GatewayID,
		"ready": ready,
	}
	if !ready {
		item["last_error"] = map[string]any{
			"code":    "GATEWAY_NOT_READY",
			"message": "Gateway readiness is unavailable.",
		}
	}
	return map[string]any{
		"ready":       ready,
		"gateways":    []map[string]any{item},
		"captured_at": time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
	}, nil
}

func (s *Server) projectsPublic(_ context.Context, raw json.RawMessage) (any, error) {
	var input struct {
		Gateway string `json:"gateway"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	if input.Gateway == "" || input.Gateway != s.Service.Config.GatewayID {
		return nil, fmt.Errorf("unknown gateway %q", input.Gateway)
	}
	resolution, err := s.Service.EffectiveProjectSnapshot()
	if err != nil {
		return nil, fmt.Errorf("projects unavailable: %w", err)
	}
	ids := make([]string, 0, len(resolution.Projects))
	for id := range resolution.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	projects := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		projects = append(projects, map[string]any{"key": id, "name": id})
	}
	return map[string]any{
		"gateway":  map[string]any{"key": input.Gateway},
		"projects": projects,
	}, nil
}
