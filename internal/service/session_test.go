package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestServiceSessionLifecycleUsesRegisteredProject(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	s := New(config.Config{StateDir: state, Projects: map[string]config.ProjectConfig{
		"example": {Root: t.TempDir(), Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"},
	}})
	started, err := s.SessionStart(context.Background(), SessionStartInput{ProjectID: "example", Role: "delivery", SessionType: "chatgpt"})
	if err != nil {
		t.Fatal(err)
	}
	if started.Session.ProjectID != "example" || started.Session.Role != "delivery" || started.Session.Status != "active" {
		t.Fatalf("start result=%#v", started)
	}
	info, err := s.SessionInfo(context.Background(), started.Session.ID)
	if err != nil || info.Session.ID != started.Session.ID {
		t.Fatalf("info result=%#v err=%v", info, err)
	}
	if _, err := s.SessionStart(context.Background(), SessionStartInput{ProjectID: "missing", Role: "delivery", SessionType: "chatgpt"}); err == nil {
		t.Fatal("unknown project session start accepted")
	}
}
