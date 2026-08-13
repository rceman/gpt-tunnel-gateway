package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

type RuntimeLogsInput struct {
	Limit       int    `json:"limit,omitempty"`
	Level       string `json:"level,omitempty"`
	Component   string `json:"component,omitempty"`
	Event       string `json:"event,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type RuntimeLogsResult struct {
	Events         []runtime_log.Event `json:"events"`
	MalformedLines int                 `json:"malformed_lines"`
}

func (s *Service) RuntimeLogs(ctx context.Context, input RuntimeLogsInput) (RuntimeLogsResult, error) {
	_ = ctx
	result, err := runtime_log.New(s.Config.StateDir).Read(runtime_log.Filter{
		Limit: input.Limit, Level: input.Level, Component: input.Component,
		Event: input.Event, OperationID: input.OperationID,
	})
	if err != nil {
		return RuntimeLogsResult{}, err
	}
	return RuntimeLogsResult{
		Events:         result.Events,
		MalformedLines: result.MalformedLines,
	}, nil
}
