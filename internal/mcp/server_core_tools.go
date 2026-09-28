package mcp

import (
	"context"
	"encoding/json"
	"time"
)

func (s *Server) addCoreTools(add toolAdder) {
	add("system_ping", "Return gateway identity and time.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.6.15", "gateway_id": s.Service.Config.GatewayID, "time": time.Now().UTC()}, nil
	})
	add("session", "Create and manage explicit durable project-bound sessions.", sessionInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionAction(ctx, raw)
	})
}
