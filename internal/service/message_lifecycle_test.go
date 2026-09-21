package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func tsk589Session(t *testing.T, s *Service, projectID, projectCode, role string) string {
	t.Helper()
	input := durableSession.CreateInput{ProjectID: projectID, ProjectCode: projectCode, Role: role, SessionType: durableSession.SessionTypeChatGPT}
	if durableSession.WorkflowRoleRequiresRef(role) {
		ref := "message-runtime-" + role
		input.SessionRef = &ref
	}
	record, err := durableSession.NewStoreWithDurability(s.Durability).Create(input)
	if err != nil {
		t.Fatal(err)
	}
	return record.ID
}

func TestTSK589PLAWMessageLifecycleRoutesIsolationBoundsAndPaging(t *testing.T) {
	s, _ := tsk585Setup(t)
	roles := model.MessageRoles()
	sessions := make(map[string]string, len(roles))
	for _, role := range roles {
		sessions[role] = tsk589Session(t, s, "example", "EXM", role)
	}
	var notifications []MessageNotification
	var notificationMu sync.Mutex
	s.SetMessageNotifier(func(_ context.Context, notification MessageNotification) error {
		notificationMu.Lock()
		notifications = append(notifications, notification)
		notificationMu.Unlock()
		if _, err := s.Durability.ReadPLAWMessage(context.Background(), notification.MessageID); err != nil {
			t.Fatalf("notification ran before durable commit: %v", err)
		}
		return nil
	})

	created := make(map[string]model.Message)
	for _, fromRole := range roles {
		for _, toRole := range roles {
			message, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions[fromRole]), MessageCreateInput{
				ToRole: toRole,
				Body:   fromRole + " to " + toRole,
			})
			if err != nil {
				t.Fatalf("route %s->%s: %v", fromRole, toRole, err)
			}
			if message.FromRole != fromRole || message.FromSession != sessions[fromRole] || message.ToRole != toRole || message.ProjectID != "example" || message.ProjectCode != "EXM" || message.State != model.MessageStateUnread {
				t.Fatalf("forged or incomplete route provenance: %#v", message)
			}
			created[fromRole+":"+toRole] = message
		}
	}
	if len(notifications) < len(created) {
		t.Fatalf("notification count=%d messages=%d", len(notifications), len(created))
	}
	for _, notification := range notifications {
		want := "Read message "
		if notification.TargetRole == durableSession.RolePlanner {
			want = "New message "
		}
		if notification.Text != want+notification.MessageID {
			t.Fatalf("notification text=%q target=%s", notification.Text, notification.TargetRole)
		}
	}
	for _, fromRole := range roles {
		for _, toRole := range roles {
			message := created[fromRole+":"+toRole]
			read, err := s.MessageRead(WithAgentSessionID(context.Background(), sessions[toRole]), message.ID)
			if err != nil || read.State != model.MessageStateRead || read.ReadSession != sessions[toRole] || read.ReadAt == nil {
				t.Fatalf("route %s->%s read=%#v err=%v", fromRole, toRole, read, err)
			}
			repeated, err := s.MessageRead(WithAgentSessionID(context.Background(), sessions[toRole]), message.ID)
			if err != nil || repeated.ReadAt == nil || repeated.ReadSession != sessions[toRole] {
				t.Fatalf("idempotent read route %s->%s result=%#v err=%v", fromRole, toRole, repeated, err)
			}
		}
	}
	for _, role := range roles {
		page, err := s.MessageList(WithAgentSessionID(context.Background(), sessions[role]), MessageListInput{})
		if err != nil || len(page.Messages) != len(roles) {
			t.Fatalf("role %s inbox page=%#v err=%v", role, page, err)
		}
	}

	parent, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["planner"]), MessageCreateInput{
		ToRole: "worker",
		Title:  "parent",
		Body:   "parent body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MessageRead(WithAgentSessionID(context.Background(), sessions["planner"]), parent.ID); err == nil {
		t.Fatal("message sender read a message addressed to another role")
	}
	reply, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["worker"]), MessageCreateInput{
		ToRole:    "planner",
		Body:      "reply body",
		InReplyTo: parent.ID,
	})
	if err != nil || reply.InReplyTo != parent.ID {
		t.Fatalf("reply linking reply=%#v err=%v", reply, err)
	}
	if _, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["worker"]), MessageCreateInput{
		ToRole:    "planner",
		Body:      "cross",
		InReplyTo: "OTH-MSG1",
	}); err == nil {
		t.Fatal("unknown or cross-project reply accepted")
	}

	if _, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["planner"]), MessageCreateInput{
		ToRole: "worker",
		Body:   strings.Repeat("x", model.MessageBodyMaxBytes+1),
	}); err == nil {
		t.Fatal("oversized body accepted")
	}
	if _, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["planner"]), MessageCreateInput{
		ToRole: "worker",
		Body:   "bounded",
		Title:  strings.Repeat("界", model.MessageTitleMaxChars+1),
	}); err == nil {
		t.Fatal("oversized Unicode title accepted")
	}
	if _, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["planner"]), MessageCreateInput{
		ProjectID: "other",
		ToRole:    "worker",
		Body:      "forged project",
	}); err == nil {
		t.Fatal("caller-selected project was accepted")
	}

	other := tsk589Session(t, s, "other", "OTH", "worker")
	if _, err := s.MessageRead(WithAgentSessionID(context.Background(), other), parent.ID); err == nil {
		t.Fatal("cross-project read succeeded")
	}
	otherPage, err := s.MessageList(WithAgentSessionID(context.Background(), other), MessageListInput{})
	if err != nil || len(otherPage.Messages) != 0 {
		t.Fatalf("cross-project list exposed messages=%#v err=%v", otherPage, err)
	}

	cancelBySender, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["worker"]), MessageCreateInput{
		ToRole: "lead",
		Body:   "cancel by sender",
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.MessageCancel(WithAgentSessionID(context.Background(), sessions["worker"]), cancelBySender.ID)
	if err != nil || cancelled.State != model.MessageStateCancelled || cancelled.CancelledBy != sessions["worker"] || cancelled.CancelledAt == nil {
		t.Fatalf("sender cancel=%#v err=%v", cancelled, err)
	}
	if _, err := s.MessageCancel(WithAgentSessionID(context.Background(), sessions["worker"]), cancelBySender.ID); err == nil {
		t.Fatal("repeated cancellation succeeded")
	}
	if _, err := s.MessageRead(WithAgentSessionID(context.Background(), sessions["lead"]), cancelBySender.ID); err == nil {
		t.Fatal("cancelled message became readable")
	}
	cancelByPlanner, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["worker"]), MessageCreateInput{
		ToRole: "lead",
		Body:   "cancel by planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MessageCancel(WithAgentSessionID(context.Background(), sessions["lead"]), cancelByPlanner.ID); err == nil {
		t.Fatal("receiver cancelled another sender message")
	}
	if result, err := s.MessageCancel(WithAgentSessionID(context.Background(), sessions["planner"]), cancelByPlanner.ID); err != nil || result.State != model.MessageStateCancelled {
		t.Fatalf("same-project Planner cancel=%#v err=%v", result, err)
	}
	readThenCancel, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["planner"]), MessageCreateInput{
		ToRole: "worker",
		Body:   "read then cancel",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MessageRead(WithAgentSessionID(context.Background(), sessions["worker"]), readThenCancel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MessageCancel(WithAgentSessionID(context.Background(), sessions["planner"]), readThenCancel.ID); err == nil {
		t.Fatal("late cancellation mutated a read message")
	}

	for i := 0; i < model.MessagePageMax+1; i++ {
		if _, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["planner"]), MessageCreateInput{
			ToRole: "worker",
			Body:   "page",
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.MessageList(WithAgentSessionID(context.Background(), sessions["worker"]), MessageListInput{UnreadOnly: true})
	if err != nil || len(first.Messages) != model.MessagePageMax || first.NextCursor == "" {
		t.Fatalf("first bounded page=%#v err=%v", first, err)
	}
	second, err := s.MessageList(WithAgentSessionID(context.Background(), sessions["worker"]), MessageListInput{
		UnreadOnly: true,
		Cursor:     first.NextCursor,
	})
	if err != nil || len(second.Messages) == 0 || len(second.Messages) > model.MessagePageMax {
		t.Fatalf("cursor page=%#v err=%v", second, err)
	}
	if first.Messages[len(first.Messages)-1].ID == second.Messages[0].ID {
		t.Fatal("cursor repeated the boundary message")
	}
	if _, err := s.MessageList(WithAgentSessionID(context.Background(), other), MessageListInput{
		UnreadOnly: true,
		Cursor:     first.NextCursor,
	}); err == nil {
		t.Fatal("cross-project cursor was accepted")
	}

	s.SetMessageNotifier(func(context.Context, MessageNotification) error { return errors.New("notification unavailable") })
	preserved, err := s.MessageCreate(WithAgentSessionID(context.Background(), sessions["planner"]), MessageCreateInput{
		ToRole: "worker",
		Body:   "notification failure must preserve",
	})
	if err != nil {
		t.Fatalf("notification failure changed create result: %v", err)
	}
	stored, err := s.Durability.ReadPLAWMessage(context.Background(), preserved.ID)
	if err != nil || stored.ID != preserved.ID || stored.State != model.MessageStateUnread {
		t.Fatalf("notification failure lost message=%#v err=%v", stored, err)
	}
}

func TestTSK589ConcurrentReadAndCancelAreAtomic(t *testing.T) {
	s, _ := tsk585Setup(t)
	planner := tsk589Session(t, s, "example", "EXM", durableSession.RolePlanner)
	worker := tsk589Session(t, s, "example", "EXM", durableSession.RoleWorker)
	message, err := s.MessageCreate(WithAgentSessionID(context.Background(), planner), MessageCreateInput{
		ToRole: "worker",
		Body:   "race",
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.MessageRead(WithAgentSessionID(context.Background(), worker), message.ID)
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.MessageCancel(WithAgentSessionID(context.Background(), planner), message.ID)
		}()
	}
	wg.Wait()
	stored, err := s.Durability.ReadPLAWMessage(context.Background(), message.ID)
	if err != nil || (stored.State != model.MessageStateRead && stored.State != model.MessageStateCancelled) {
		t.Fatalf("atomic terminal state=%#v err=%v", stored, err)
	}
	if stored.State == model.MessageStateRead && stored.ReadAt == nil {
		t.Fatal("read terminal state omitted read_at")
	}
}
