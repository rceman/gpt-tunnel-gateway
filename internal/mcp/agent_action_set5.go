package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) agentActionSet5() error {
	register := func(action GenericAction) error { return s.RegisterGenericAction(action) }
	if err := register(GenericAction{
		Path:         "agent/prompt_queue",
		Description:  "List unread Planner Message Tokens for the bound Agent target.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "limit": integer("Maximum queue entries.", 1, model.MaxPMTQueueEntries)}, "project_id"),
		OutputSchema: map[string]any{"type": "object", "additionalProperties": true},
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Limit     int    `json:"limit"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			if in.Limit == 0 {
				in.Limit = model.MaxPMTQueueEntries
			}
			return s.Service.PMTQueue(ctx, in.ProjectID, in.Limit)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/prompt_cancel",
		Description:  "Cancel one unread Planner Message Token atomically.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "pmt_id": str("Exact PMT identifier.")}, "project_id", "pmt_id"),
		OutputSchema: map[string]any{"type": "object", "additionalProperties": true},
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				PMTID     string `json:"pmt_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.PMTCancel(ctx, in.ProjectID, in.PMTID)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "agent/prompt_supersede",
		Description:  "Atomically supersede unread PMTs and deliver one replacement reference.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "pmt_ids": array(str("Exact PMT identifier.")), "title": str("Bounded replacement title."), "message": boundedAgentMessageSchema()}, "project_id", "pmt_ids", "message"),
		OutputSchema: agentIPCReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string   `json:"project_id"`
				PMTIDs    []string `json:"pmt_ids"`
				Title     string   `json:"title"`
				Message   string   `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.PMTSupersede(ctx, service.PMTSupersedeInput{ProjectID: in.ProjectID, OldIDs: in.PMTIDs, Title: in.Title, Message: in.Message})
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:         "agent/prompt_read",
		Description:  "Read one PMT body from the exact bound coding Agent session.",
		InputSchema:  obj(map[string]any{"pmt_id": str("Exact PMT identifier.")}, "pmt_id"),
		OutputSchema: map[string]any{"type": "object", "additionalProperties": true},
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    "",
		SessionBound:     true,
		SessionRequired:  true,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				PMTID string `json:"pmt_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.PMTRead(ctx, in.PMTID)
		},
	})
}
