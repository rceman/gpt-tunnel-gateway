package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureCallbackActions() {
	if s.Service == nil {
		return
	}
	s.callbackActions.Do(func() {
		s.callbackActionErr = s.registerCallbackActions()
	})
	if s.callbackActionErr != nil {
		panic(s.callbackActionErr)
	}
}

func callbackURLInputSchema() map[string]any {
	method := map[string]any{"type": "string", "enum": []any{"POST", "PUT"}}
	urlValue := str("Absolute HTTP or HTTPS callback URL.")
	urlValue["minLength"], urlValue["maxLength"] = 1, model.MaxProjectCallbackURLBytes
	body := str("Exact callback request body.")
	body["maxLength"] = model.MaxProjectCallbackBodyBytes
	return obj(map[string]any{"method": method, "url": urlValue, "body": body}, "method", "url", "body")
}

func callbackScriptInputSchema() map[string]any {
	path := str("Repository-relative callback script path.")
	path["minLength"], path["maxLength"] = 1, model.MaxProjectCallbackScriptPathBytes
	arg := str("One bounded script argument.")
	arg["maxLength"] = model.MaxProjectCallbackArgBytes
	args := array(arg)
	args["maxItems"] = model.MaxProjectCallbackScriptArgs
	return obj(map[string]any{"path": path, "args": args}, "path", "args")
}

func callbackRegisterInputSchema() map[string]any {
	callback := str("Unique project-scoped callback key.")
	callback["minLength"], callback["maxLength"] = 1, model.MaxProjectCallbackKeyBytes
	event := map[string]any{"type": "string", "enum": []any{model.ProjectCallbackWorkFinishedEvent}}
	properties := map[string]any{"callback": callback, "event": event, "url": callbackURLInputSchema(), "script": callbackScriptInputSchema()}
	schema := obj(properties, "callback", "event")
	schema["anyOf"] = []any{
		obj(properties, "callback", "event", "url"),
		obj(properties, "callback", "event", "script"),
	}
	return schema
}

func callbackRemoveInputSchema() map[string]any {
	callback := str("Exact project-scoped callback key.")
	callback["minLength"], callback["maxLength"] = 1, model.MaxProjectCallbackKeyBytes
	return obj(map[string]any{"callback": callback}, "callback")
}

func callbackListInputSchema() map[string]any { return obj(map[string]any{}) }

func callbackSummaryOutputSchema() map[string]any {
	url := closedOutput(map[string]any{"method": outputEnum("POST", "PUT"), "url": outputString()}, "method", "url")
	script := closedOutput(map[string]any{"path": outputString()}, "path")
	return closedOutput(map[string]any{"key": outputString(), "event": outputString(), "url": url, "script": script}, "key", "event")
}

func callbackRegistrationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"callback": outputString(), "event": outputString(), "status": outputEnum("registered", "already_registered", "removed")}, "callback", "event", "status")
}

func callbackListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"callbacks": outputArray(callbackSummaryOutputSchema())}, "callbacks")
}

func callbackEventsOutputSchema() map[string]any {
	event := closedOutput(map[string]any{"key": outputString(), "description": outputString()}, "key", "description")
	return closedOutput(map[string]any{"events": outputArray(event)}, "events")
}

func (s *Server) registerCallbackActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:            "callback/events",
		Description:     "List the bounded project callback events supported by Gateway.",
		InputSchema:     callbackListInputSchema(),
		OutputSchema:    callbackEventsOutputSchema(),
		Annotations:     ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole:   actionRolePlannerOrDelivery,
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return s.Service.CallbackEvents(ctx)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:            "callback/list",
		Description:     "List deterministic compact callback registrations for the bound project.",
		InputSchema:     callbackListInputSchema(),
		OutputSchema:    callbackListOutputSchema(),
		Annotations:     ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole:   actionRolePlannerOrDelivery,
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			projectID, err := boundCallbackProject(s, ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.CallbackList(ctx, projectID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:            "callback/register",
		Description:     "Register one bounded project callback for an Agent work-finished event.",
		InputSchema:     callbackRegisterInputSchema(),
		OutputSchema:    callbackRegistrationOutputSchema(),
		Annotations:     ToolAnnotations{DestructiveHint: true, IdempotentHint: true},
		AuthorityRole:   "planner",
		SessionBound:    true,
		SessionRequired: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var callback model.ProjectCallback
			if err := decode(raw, &callback); err != nil {
				return nil, err
			}
			projectID, err := boundCallbackProject(s, ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.CallbackRegister(ctx, projectID, service.CallbackRegisterInput{Callback: callback})
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:            "callback/remove",
		Description:     "Remove one exact project callback registration.",
		InputSchema:     callbackRemoveInputSchema(),
		OutputSchema:    callbackRegistrationOutputSchema(),
		Annotations:     ToolAnnotations{DestructiveHint: true, IdempotentHint: true},
		AuthorityRole:   "planner",
		SessionBound:    true,
		SessionRequired: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input service.CallbackRemoveInput
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := boundCallbackProject(s, ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.CallbackRemove(ctx, projectID, input)
		},
	})
}

func boundCallbackProject(s *Server, ctx context.Context) (string, error) {
	sessionID := service.AgentSessionID(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("callback action requires a bound session")
	}
	record, err := s.activeSession(sessionID)
	if err != nil {
		return "", err
	}
	if record.ProjectID == "" {
		return "", fmt.Errorf("callback action requires a bound project")
	}
	return record.ProjectID, nil
}
