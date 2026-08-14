package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

type RuntimeLogsInput struct {
	Limit       int    `json:"limit,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
	Level       string `json:"level,omitempty"`
	Component   string `json:"component,omitempty"`
	Event       string `json:"event,omitempty"`
	Action      string `json:"action,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type RuntimeLogsResult struct {
	Events         []runtime_log.Event `json:"events"`
	MalformedLines int                 `json:"malformed_lines"`
	NextCursor     string              `json:"next_cursor"`
	HasMore        bool                `json:"has_more"`
}

func (s *Service) RuntimeLogs(ctx context.Context, input RuntimeLogsInput) (RuntimeLogsResult, error) {
	_ = ctx
	result, err := runtime_log.New(s.Config.StateDir).Read(runtime_log.Filter{
		Limit: input.Limit, Cursor: input.Cursor, Level: input.Level, Component: input.Component,
		Event: input.Event, Action: input.Action, RequestID: input.RequestID,
		SessionID: input.SessionID, ProjectID: input.ProjectID, OperationID: input.OperationID,
	})
	if err != nil {
		return RuntimeLogsResult{}, err
	}
	return RuntimeLogsResult{
		Events:         result.Events,
		MalformedLines: result.MalformedLines,
		NextCursor:     result.NextCursor,
		HasMore:        result.HasMore,
	}, nil
}
