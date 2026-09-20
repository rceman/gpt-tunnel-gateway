package service

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK480TaskDispatchRequiresCanonicalPriority(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	task, _, err := s.taskAuthoringCreateShared(context.Background(), "tsk480-dispatch-priority", TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Dispatch priority",
		Summary:     "Dispatch priority summary.",
		Objective:   "Require priority before execution.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskExecutionDispatch(context.Background(), TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       task.ID,
	}); err == nil || !strings.Contains(err.Error(), "priority") {
		t.Fatalf("dispatch without priority error=%v", err)
	}
}
