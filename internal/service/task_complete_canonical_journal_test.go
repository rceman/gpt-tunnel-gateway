package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTaskCompleteAcceptsCanonicalPlannerNotesReview(t *testing.T) {
	s, _, _ := testService(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	s.Durability = db
	task := model.TaskAuthoring{ID: "GTW-TSK700"}
	sessionID := tsk585PlannerSession(t, s)
	data, err := json.Marshal(map[string]any{
		"summary": "final acceptance", "decisions": []string{}, "commitments": []string{},
		"facts": []string{"all acceptance criteria are satisfied"}, "assumptions": []string{},
		"blockers": []string{}, "unresolved": []string{}, "next_actions": []string{}, "references": []string{task.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := model.JournalEntry{
		ProjectID: "example", Stream: model.JournalStreamPlannerNotes, Role: durableSession.RolePlanner,
		SessionID: sessionID, Data: data,
	}
	if err := s.taskCompleteCanonicalReviewProof(context.Background(), TaskCompleteInput{
		ProjectID: "example",
		Review:    "GTW-JRN700",
	}, task, "EXM", entry); err != nil {
		t.Fatal(err)
	}
	entry.Stream = model.JournalStreamWorkerLessons
	if err := s.taskCompleteCanonicalReviewProof(context.Background(), TaskCompleteInput{ProjectID: "example"}, task, "EXM", entry); err == nil || !strings.Contains(err.Error(), "planner-notes") {
		t.Fatalf("wrong stream accepted: %v", err)
	}
}
