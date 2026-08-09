package service

import (
	"context"
	"strings"
	"testing"
)

func TestTaskCancelIgnoresHistoricalAwaitingResult(t *testing.T) {
	s, revision, projectHead := testService(t)
	task, revision, _, _ := installHistoricalOnlyDispatchedTask(t, s, revision, projectHead, "33333333-3333-4333-8333-333333333333", "feature/history-cancel")
	result, err := s.TaskCancel(context.Background(), task.ID, revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "cancelled" {
		t.Fatalf("status=%s", result.Status)
	}
}

func TestStateRepairRefusesWhenAnOperationalRunExists(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "feature/operational-run")
	result, err := s.StateRepair(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 0 {
		t.Fatalf("operational task was eligible for historical repair: %#v", result.Actions)
	}
}

func TestTaskReadDoesNotSelectHistoricalAwaitingResult(t *testing.T) {
	s, revision, projectHead := testService(t)
	task, _, _, _ := installHistoricalOnlyDispatchedTask(t, s, revision, projectHead, "44444444-4444-4444-8444-444444444444", "feature/history-read")
	if _, err := s.TaskRead(context.Background(), task.ID); err == nil || !strings.Contains(err.Error(), "found 0") {
		t.Fatalf("historical run became an execution packet: %v", err)
	}
}
