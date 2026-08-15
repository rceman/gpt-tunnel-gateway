package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) ensureWatcherActions() {
	if s.Service == nil {
		return
	}
	s.watcherActions.Do(func() {
		s.watcherActionErr = s.registerWatcherActions()
	})
	if s.watcherActionErr != nil {
		panic(s.watcherActionErr)
	}
}

func watcherGuideInputSchema() map[string]any {
	return obj(map[string]any{
		"schema_version": integer("Guide schema version.", 1, 1),
		"project_id":     str("Registered project identifier."),
		"revision":       integer("Monotonic guide revision.", 1, int(model.MaxSafeInteger)),
		"content":        str("Complete bounded watcher guide content."),
		"updated_by":     str("Planner identity that authorized the guide."),
		"updated_at":     str("RFC3339 guide update timestamp."),
	}, "schema_version", "project_id", "revision", "content", "updated_by", "updated_at")
}

func watcherObjectOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func (s *Server) registerWatcherActions() error {
	register := func(action GenericAction) error {
		if action.AuthorityRole == "" {
			action.AuthorityRole = durableSession.RoleDelivery
		}
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:         "watcher/watch",
		Description:  "Observe one bounded project watcher tick.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "lines": integer("Tail lines.", 1, model.WatcherMaxTailLines)}, "project_id"),
		OutputSchema: watcherObjectOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: durableSession.RoleDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.WatcherObserveInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.WatcherObserve(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "watcher/nudge",
		Description:  "Deliver one bounded nudge to the exact active Run session.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier."), "text": str("Short bounded nudge.")}, "project_id", "text"),
		OutputSchema: watcherObjectOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole: durableSession.RoleDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.WatcherNudgeInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.WatcherNudge(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "watcher/status",
		Description:  "Read one bounded Gateway watcher status projection.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier.")}, "project_id"),
		OutputSchema: watcherObjectOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: durableSession.RoleDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.WatcherStatus(ctx, in.ProjectID)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "watcher/guide",
		Description:  "Read the one revisioned Gateway watcher guide.",
		InputSchema:  obj(map[string]any{"project_id": str("Registered project identifier.")}, "project_id"),
		OutputSchema: watcherObjectOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: durableSession.RoleDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.WatcherGuideRead(ctx, in.ProjectID)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:        "watcher/guide_update",
		Description: "Update the one revisioned Gateway watcher guide with optimistic Hub revision guarding.",
		InputSchema: obj(map[string]any{
			"project_id":            str("Registered project identifier."),
			"guide":                 watcherGuideInputSchema(),
			"expected_hub_revision": str("Optional exact Hub revision guard."),
		}, "project_id", "guide"),
		OutputSchema: watcherGuideMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole: durableSession.RolePlanner,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.WatcherGuideUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			if in.Guide.ProjectID == "" {
				in.Guide.ProjectID = in.ProjectID
			}
			return s.Service.WatcherGuideUpdateAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:         "watcher/guide_update_status",
		Description:  "Read the durable receipt for an asynchronous watcher guide update.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable watcher guide operation identifier.")}, "operation_id"),
		OutputSchema: watcherGuideMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    durableSession.RolePlanner,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.WatcherGuideUpdateOperationStatus(ctx, in.OperationID)
		},
	})
}
