package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureRuntimeLogActions() {
	if s.Service == nil {
		return
	}
	s.runtimeLogActions.Do(func() {
		s.runtimeLogActionErr = s.RegisterGenericAction(GenericAction{
			Path:        "runtime/logs",
			Description: "Read bounded structured Gateway lifecycle and action events.",
			InputSchema: obj(map[string]any{
				"limit":        integer("Maximum events to return.", 1, runtime_log.MaxLimit),
				"level":        str("Exact event level filter."),
				"component":    str("Exact component filter."),
				"event":        str("Exact event name filter."),
				"operation_id": str("Exact durable operation correlation filter."),
			}),
			OutputSchema: runtimeLogsOutputSchema(),
			Annotations:  readOnlyAnnotations(),
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var input service.RuntimeLogsInput
				if err := decode(raw, &input); err != nil {
					return nil, err
				}
				return s.Service.RuntimeLogs(ctx, input)
			},
		})
	})
	if s.runtimeLogActionErr != nil {
		panic(s.runtimeLogActionErr)
	}
}

func runtimeLogsOutputSchema() map[string]any {
	event := closedOutput(map[string]any{
		"timestamp": outputDateTime(), "level": outputString(), "component": outputString(), "event": outputString(),
		"action": outputString(), "request_id": outputString(), "operation_id": outputString(), "session_id": outputString(),
		"project_id": outputString(), "pid": outputInteger(), "start_time_ticks": outputInteger(), "source": outputString(),
		"version": outputString(), "signal": outputString(), "message": outputString(), "error": outputString(),
	}, "timestamp", "level", "component", "event")
	return closedOutput(map[string]any{
		"events": outputArray(event), "malformed_lines": outputInteger(),
	}, "events", "malformed_lines")
}
