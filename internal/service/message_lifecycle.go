package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type MessageCreateInput struct {
	ProjectID string
	ToRole    string
	Body      string
	Title     string
	InReplyTo string
}

type MessageListInput struct {
	ProjectID  string
	UnreadOnly bool
	Cursor     string
}

type MessageListResult struct {
	Messages   []model.Message `json:"messages"`
	NextCursor string          `json:"-"`
	HasMore    bool            `json:"-"`
}

type MessageNotification struct {
	MessageID     string
	ProjectID     string
	TargetRole    string
	TargetSession string
	Text          string
}

func (s *Service) SetMessageNotifier(notifier func(context.Context, MessageNotification) error) {
	s.messageNotifier = notifier
}

func (s *Service) messageActor(ctx context.Context) (durableSession.Record, error) {
	id := AgentSessionID(ctx)
	if id == "" {
		return durableSession.Record{}, fmt.Errorf("authenticated Session is required")
	}
	if s.Durability == nil {
		return durableSession.Record{}, fmt.Errorf("local Session authority is unavailable")
	}
	record, err := durableSession.NewStoreWithDurability(s.Durability).Get(id)
	if err != nil {
		return durableSession.Record{}, fmt.Errorf("authenticated Session is invalid: %w", err)
	}
	if record.Status != durableSession.StatusActive || !durableSession.IsWorkflowRole(record.Role) || record.ProjectID == "" || record.ProjectCode == "" {
		return durableSession.Record{}, fmt.Errorf("authenticated Session is not an active PLAW Session")
	}
	return record, nil
}

func (s *Service) MessageCreate(ctx context.Context, in MessageCreateInput) (model.Message, error) {
	if s.Durability == nil {
		return model.Message{}, fmt.Errorf("message durability is unavailable")
	}
	actor, err := s.messageActor(ctx)
	if err != nil {
		return model.Message{}, err
	}
	if in.ProjectID != "" && in.ProjectID != actor.ProjectID {
		return model.Message{}, fmt.Errorf("message project is derived from the authenticated Session")
	}
	if !model.IsMessageRole(in.ToRole) {
		return model.Message{}, fmt.Errorf("unsupported message recipient role %q", in.ToRole)
	}
	if len([]byte(in.Body)) == 0 || len([]byte(in.Body)) > model.MessageBodyMaxBytes {
		return model.Message{}, fmt.Errorf("message body must be between 1 and %d bytes", model.MessageBodyMaxBytes)
	}
	if !utf8.ValidString(in.Body) || !utf8.ValidString(in.Title) {
		return model.Message{}, fmt.Errorf("message text must be valid UTF-8")
	}
	if in.Title != "" && len([]rune(in.Title)) > model.MessageTitleMaxChars {
		return model.Message{}, fmt.Errorf("message title exceeds %d characters", model.MessageTitleMaxChars)
	}
	if strings.ContainsRune(in.Body, '\x00') || strings.ContainsRune(in.Title, '\x00') {
		return model.Message{}, fmt.Errorf("message text contains NUL")
	}
	if in.InReplyTo != "" {
		if err := model.ValidateMessageID(in.InReplyTo); err != nil {
			return model.Message{}, fmt.Errorf("invalid reply message: %w", err)
		}
		parent, readErr := s.Durability.ReadPLAWMessage(ctx, in.InReplyTo)
		if readErr != nil {
			return model.Message{}, readErr
		}
		if parent.ProjectID != actor.ProjectID {
			return model.Message{}, fmt.Errorf("reply message belongs to another project")
		}
	}
	created, err := s.Durability.CreatePLAWMessage(ctx, model.Message{
		SchemaVersion: model.MessageSchemaVersion,
		ProjectID:     actor.ProjectID,
		ProjectCode:   actor.ProjectCode,
		FromRole:      actor.Role,
		FromSession:   actor.ID,
		ToRole:        in.ToRole,
		Body:          in.Body,
		Title:         in.Title,
		InReplyTo:     in.InReplyTo,
		State:         model.MessageStateUnread,
		CreatedAt:     s.durableNow(),
	})
	if err != nil {
		return model.Message{}, err
	}
	if notifyErr := s.notifyMessage(ctx, created); notifyErr != nil {
		s.recordMessageNotificationFailure(ctx, created, notifyErr)
	}
	return created, nil
}

func (s *Service) MessageRead(ctx context.Context, messageID string) (model.Message, error) {
	actor, err := s.messageActor(ctx)
	if err != nil {
		return model.Message{}, err
	}
	if err := model.ValidateMessageID(messageID); err != nil {
		return model.Message{}, err
	}
	return s.Durability.MarkPLAWMessageRead(ctx, messageID, actor.ProjectID, actor.Role, actor.ID, s.durableNow())
}

func (s *Service) MessageList(ctx context.Context, in MessageListInput) (MessageListResult, error) {
	actor, err := s.messageActor(ctx)
	if err != nil {
		return MessageListResult{}, err
	}
	if in.ProjectID != "" && in.ProjectID != actor.ProjectID {
		return MessageListResult{}, fmt.Errorf("message project is derived from the authenticated Session")
	}
	kind := "plaw-message-list:" + actor.ProjectID + ":" + actor.Role
	if in.UnreadOnly {
		kind += ":unread"
	}
	var afterCreatedAt, afterID string
	if in.Cursor != "" {
		key, decodeErr := pagination.DecodeOpaqueKeyset(in.Cursor, kind)
		if decodeErr != nil {
			return MessageListResult{}, decodeErr
		}
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return MessageListResult{}, fmt.Errorf("invalid message cursor")
		}
		afterCreatedAt, afterID = parts[0], parts[1]
	}
	messages, hasMore, err := s.Durability.ListPLAWMessages(ctx, actor.ProjectID, actor.Role, in.UnreadOnly, afterCreatedAt, afterID, model.MessagePageMax)
	if err != nil {
		return MessageListResult{}, err
	}
	result := MessageListResult{
		Messages: messages,
		HasMore:  hasMore,
	}
	if hasMore && len(messages) > 0 {
		last := messages[len(messages)-1]
		result.NextCursor = pagination.EncodeOpaqueKeyset(kind, last.CreatedAt.UTC().Format(time.RFC3339Nano)+"\x00"+last.ID)
	}
	return result, nil
}

func (s *Service) MessageCancel(ctx context.Context, messageID string) (model.Message, error) {
	actor, err := s.messageActor(ctx)
	if err != nil {
		return model.Message{}, err
	}
	if err := model.ValidateMessageID(messageID); err != nil {
		return model.Message{}, err
	}
	return s.Durability.CancelPLAWMessage(ctx, messageID, actor.ProjectID, actor.ID, actor.Role, s.durableNow())
}

func (s *Service) notifyMessage(ctx context.Context, message model.Message) error {
	text := "Read message " + message.ID
	if message.ToRole == "planner" {
		text = "New message " + message.ID
	}
	store := durableSession.NewStoreWithDurability(s.Durability)
	records, err := store.List()
	if err != nil {
		return err
	}
	notified := false
	for _, record := range records {
		if record.Status != durableSession.StatusActive || record.ProjectID != message.ProjectID || record.Role != message.ToRole {
			continue
		}
		notified = true
		notification := MessageNotification{
			MessageID:     message.ID,
			ProjectID:     message.ProjectID,
			TargetRole:    message.ToRole,
			TargetSession: record.ID,
			Text:          text,
		}
		if s.messageNotifier != nil {
			if err := s.messageNotifier(ctx, notification); err != nil {
				return err
			}
			continue
		}
		if record.SessionRef == nil || strings.TrimSpace(*record.SessionRef) == "" {
			continue
		}
		if _, err := s.Airelay.PromptWithProvenance(ctx, *record.SessionRef, message.FromSession, text); err != nil {
			return err
		}
	}
	if !notified && s.messageNotifier != nil {
		return s.messageNotifier(ctx, MessageNotification{
			MessageID:  message.ID,
			ProjectID:  message.ProjectID,
			TargetRole: message.ToRole,
			Text:       text,
		})
	}
	return nil
}

func (s *Service) recordMessageNotificationFailure(ctx context.Context, message model.Message, cause error) {
	if s.Config.StateDir == "" || cause == nil {
		return
	}
	_ = runtime_log.New(s.Config.StateDir).Append(runtime_log.Event{
		Timestamp: time.Now().UTC(),
		Level:     "warn",
		Component: "message",
		Event:     "message_notification_failed",
		Action:    "message/create",
		RequestID: runtime_log.RequestID(ctx),
		SessionID: message.FromSession,
		ProjectID: message.ProjectID,
		Message:   "durable message committed before notification failure",
		Error:     runtime_log.SanitizeText(cause.Error()),
	})
}
