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
	if status.Progress.AgentState != "idle" || status.Progress.Tail != "Idle prompt ready\n" || status.Progress.RecommendedNextAction != "inspect Task execution state" {
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
	if _, err := s.ProjectStatus(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
}

func TestProjectStatusDoesNotReadRetiredTaskHubFamilies(t *testing.T) {
	s, hubRevision, _ := testService(t)
	_, _, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Slug:               "status-only-task",
		Title:              "Status-only task",
		Objective:          "Ensure project status does not depend on retired Task Hub files.",
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
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range status.Progress.ComponentErrors {
		if strings.HasPrefix(component, "tasks:") {
			t.Fatalf("project status read a retired Task family: %#v", status.Progress)
		}
	}
}

func TestProjectStatusIgnoresRetiredTaskStateCorruption(t *testing.T) {
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
	for _, component := range status.Progress.ComponentErrors {
		if strings.HasPrefix(component, "tasks:") {
			t.Fatalf("project status still depends on corrupted retired Task state: %#v", status.Progress)
		}
	}
}
