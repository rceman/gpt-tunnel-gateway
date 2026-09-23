package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureMessageActions() {
	if s.Service == nil {
		return
	}
	s.messageActions.Do(func() { s.messageActionErr = s.registerMessageActions() })
	if s.messageActionErr != nil {
		panic(s.messageActionErr)
	}
}

func messageRoleSchema() map[string]any {
	return outputEnum(model.MessageRoles()...)
}

func messageCreateInputSchema() map[string]any {
	body := str("Message body, bounded in UTF-8 bytes.")
	body["minLength"], body["maxLength"] = 1, model.MessageBodyMaxBytes
	title := str("Optional message title, bounded in Unicode characters.")
	title["maxLength"] = model.MessageTitleMaxChars
	return obj(map[string]any{
		"to_role":     messageRoleSchema(),
		"body":        body,
		"title":       title,
		"in_reply_to": str("Optional same-project message identifier."),
	}, "to_role", "body")
}

func messageReadInputSchema() map[string]any {
	return obj(map[string]any{"message": str("Exact message identifier.")}, "message")
}

func messageListInputSchema() map[string]any {
	unreadOnly := map[string]any{"type": "boolean", "description": "Return only unread messages."}
	cursor := publicServerCursorSchema()
	cursor["description"] = "Opaque continuation cursor."
	return obj(map[string]any{"unread_only": unreadOnly, "cursor": cursor})
}

func messageCancelInputSchema() map[string]any {
	return obj(map[string]any{"message": str("Exact message identifier.")}, "message")
}

func messageCreateOutputSchema() map[string]any {
	return closedOutput(map[string]any{"message": outputString(), "status": outputEnum(model.MessageStateUnread, model.MessageStateRead, model.MessageStateCancelled)}, "message", "status")
}

func messageReadOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"message":      outputString(),
		"from_role":    messageRoleSchema(),
		"from_session": outputString(),
		"to_role":      messageRoleSchema(),
		"body":         outputString(),
		"title":        outputString(),
		"in_reply_to":  outputString(),
		"status":       outputEnum(model.MessageStateRead),
		"created_at":   outputDateTime(),
		"read_at":      outputDateTime(),
		"read_session": outputString(),
		"cancelled_by": outputString(),
	}, "message", "from_role", "from_session", "to_role", "body", "status", "created_at", "read_at", "read_session")
}

func messageListRowOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"message":     outputString(),
		"from_role":   messageRoleSchema(),
		"to_role":     messageRoleSchema(),
		"title":       outputString(),
		"in_reply_to": outputString(),
		"status":      outputEnum(model.MessageStateUnread, model.MessageStateRead, model.MessageStateCancelled),
		"created_at":  outputDateTime(),
	}, "message", "from_role", "to_role", "status", "created_at")
}

func messageListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"items": outputArray(messageListRowOutputSchema())}, "items")
}

func messageCancelOutputSchema() map[string]any {
	return closedOutput(map[string]any{"message": outputString(), "status": outputEnum(model.MessageStateCancelled)}, "message", "status")
}

func messageReadValue(message model.Message) map[string]any {
	value := map[string]any{
		"message":      message.ID,
		"from_role":    message.FromRole,
		"from_session": message.FromSession,
		"to_role":      message.ToRole,
		"body":         message.Body,
		"status":       message.State,
		"created_at":   message.CreatedAt,
		"read_at":      message.ReadAt,
		"read_session": message.ReadSession,
	}
	if message.Title != "" {
		value["title"] = message.Title
	}
	if message.InReplyTo != "" {
		value["in_reply_to"] = message.InReplyTo
	}
	if message.CancelledBy != "" {
		value["cancelled_by"] = message.CancelledBy
	}
	return value
}

func messageListRowValue(message model.Message) map[string]any {
	value := map[string]any{
		"message":    message.ID,
		"from_role":  message.FromRole,
		"to_role":    message.ToRole,
		"status":     message.State,
		"created_at": message.CreatedAt,
	}
	if message.Title != "" {
		value["title"] = message.Title
	}
	if message.InReplyTo != "" {
		value["in_reply_to"] = message.InReplyTo
	}
	return value
}

func (s *Server) registerMessageActions() error {
	register := func(action GenericAction) error {
		action.SessionBound = true
		action.SessionRequired = true
		action.LocalReceiptOnly = true
		action.AuthorityRole = ""
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:         "message/create",
		Description:  "Create one same-project PLAW message.",
		InputSchema:  messageCreateInputSchema(),
		OutputSchema: messageCreateOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ToRole    string `json:"to_role"`
				Body      string `json:"body"`
				Title     string `json:"title,omitempty"`
				InReplyTo string `json:"in_reply_to,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor, ok := resolvedSessionAuthorityFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("authenticated Session is required")
			}
			created, err := s.Service.MessageCreate(service.WithAgentSessionID(ctx, actor.Session.ID), service.MessageCreateInput{ProjectID: actor.Session.ProjectID, ToRole: in.ToRole, Body: in.Body, Title: in.Title, InReplyTo: in.InReplyTo})
			if err != nil {
				return nil, err
			}
			return map[string]any{"message": created.ID, "status": created.State}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "message/read",
		Description:  "Atomically read one same-project PLAW message.",
		InputSchema:  messageReadInputSchema(),
		OutputSchema: messageReadOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Message string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor, ok := resolvedSessionAuthorityFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("authenticated Session is required")
			}
			message, err := s.Service.MessageRead(service.WithAgentSessionID(ctx, actor.Session.ID), in.Message)
			if err != nil {
				return nil, err
			}
			return messageReadValue(message), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:         "message/list",
		Description:  "List the authenticated PLAW Session's bounded message inbox.",
		InputSchema:  messageListInputSchema(),
		OutputSchema: messageListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				UnreadOnly bool   `json:"unread_only,omitempty"`
				Cursor     string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor, ok := resolvedSessionAuthorityFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("authenticated Session is required")
			}
			page, err := s.Service.MessageList(service.WithAgentSessionID(ctx, actor.Session.ID), service.MessageListInput{ProjectID: actor.Session.ProjectID, UnreadOnly: in.UnreadOnly, Cursor: in.Cursor})
			if err != nil {
				return nil, err
			}
			items := make([]any, 0, len(page.Messages))
			for _, message := range page.Messages {
				items = append(items, messageListRowValue(message))
			}
			return genericActionPageResult(map[string]any{"items": items}, page.HasMore, page.NextCursor)
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:         "message/cancel",
		Description:  "Cancel one unread same-project PLAW message.",
		InputSchema:  messageCancelInputSchema(),
		OutputSchema: messageCancelOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Message string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor, ok := resolvedSessionAuthorityFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("authenticated Session is required")
			}
			message, err := s.Service.MessageCancel(service.WithAgentSessionID(ctx, actor.Session.ID), in.Message)
			if err != nil {
				return nil, err
			}
			return map[string]any{"message": message.ID, "status": message.State}, nil
		},
	})
}
