package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

func writeLivenessScript(t *testing.T, s *Service, tail string, status string, log string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n"
	body += "session-status) printf '%s\\n' '" + strings.ReplaceAll(status, "'", "'\\''") + "' ;;\n"
	body += "tail) printf '%s\\n' '" + strings.ReplaceAll(tail, "'", "'\\''") + "' ;;\n"
	body += "prompt)"
	if log != "" {
		body += " printf '%s\\n' \"$@\" >> '" + filepath.Join(dir, "calls") + "'"
	}
	body += " printf 'sent\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
}

func TestProjectStatusAggregatesProgressWithoutSessionIdentity(t *testing.T) {
	s, _, _ := testService(t)
	writeLivenessScript(t, s, "Idle prompt ready", "Controller: reachable\nState: idle", "")
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.Progress.AgentState != "idle" || status.Progress.Tail != "Idle prompt ready\n" || status.Progress.RecommendedNextAction != "inspect Train-v2 item attempt" {
		t.Fatalf("unexpected progress: %#v", status.Progress)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session_key") || strings.Contains(string(data), "airelay_session_key") || strings.Contains(string(data), s.Config.Projects["example"].Root) || strings.Contains(string(data), s.Config.Projects["example"].Mirror) {
		t.Fatalf("project status exposed session identity: %s", data)
	}
}

func TestProjectStatusDelayedComponentsCompleteConcurrently(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\nsession-status) sleep 1; printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) sleep 1; printf 'Idle prompt ready\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	started := time.Now()
	if _, err := s.ProjectStatus(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("bounded concurrent project status took too long: %s", elapsed)
	}
}

func TestProjectStatusUsesStatusOnlyTaskProjection(t *testing.T) {
	s, hubRevision, _ := testService(t)
	task, _, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Slug:               "status-only-task",
		Title:              "Status-only task",
		Objective:          "Exercise the bounded project status task projection.",
		AcceptanceCriteria: []string{"bounded"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.taskStatusList(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	var found *TaskRecord
	for i := range items {
		if items[i].Task.ID == task.ID {
			found = &items[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("status-only task projection omitted %s", task.ID)
	}
	if found.CurrentRevision != nil {
		t.Fatalf("status-only projection performed enrichment: %#v", *found)
	}
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range status.Progress.ComponentErrors {
		if component == "tasks_unavailable" {
			t.Fatalf("healthy status-only task projection became unavailable: %#v", status.Progress)
		}
	}
}

func TestProjectStatusStatusOnlyTaskFailureRemainsUnavailable(t *testing.T) {
	s, hubRevision, _ := testService(t)
	task, created, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Slug:               "status-only-failure",
		Title:              "Status-only failure",
		Objective:          "Exercise task component failure signaling.",
		AcceptanceCriteria: []string{"failure"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Hub.Transact(context.Background(), created.Hub.After, "test: corrupt task state", func(worktree string) ([]string, error) {
		path := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(worktree, path, map[string]any{
			"schema_version": 1,
			"task_id":        task.ID,
			"task_sha256":    task.SHA256,
			"status":         "not-a-task-state",
			"updated_at":     time.Now().UTC(),
		}); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range status.Progress.ComponentErrors {
		if strings.HasPrefix(component, "tasks:") {
			found = true
			break
		}
	}
	if !found || status.Progress.BlockerClassification != "PROGRESS_COMPONENT_ERROR" {
		t.Fatalf("task failure was not preserved as a component error: %#v", status.Progress)
	}
}
