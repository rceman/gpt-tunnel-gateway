package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

func (s *Server) addCoreTools(add toolAdder) {
	add("system_ping", "Return gateway identity and time.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.6.13", "gateway_id": s.Service.Config.GatewayID, "time": time.Now().UTC()}, nil
	})
	add("session", "Create and manage explicit durable project-bound sessions.", sessionInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionAction(ctx, raw)
	})
	add("gateway_capabilities", "Describe configured limits, projects, and transport.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		ids, err := s.Service.EffectiveProjectIDs()
		if err != nil {
			return nil, err
		}
		return map[string]any{"gateway_id": s.Service.Config.GatewayID, "listen_addr": s.Service.Config.ListenAddr, "projects": ids, "hub_protocol_root": hub.ProtocolRoot, "hub_repository_url": s.Service.Config.Hub.RepositoryURL, "hub_branch": s.Service.Config.Hub.Branch, "hub_managed_root": hub.ManagedRoot(s.Service.Config), "airelay_control_only": true, "generic_shell_available": false}, nil
	})
}
