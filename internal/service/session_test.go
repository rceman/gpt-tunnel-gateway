package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestServiceSessionLifecycleUsesRegisteredProject(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	hubBare, root, hubHead := testutil.RepoWithBareRemote(t)
	s := New(config.Config{StateDir: state, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"}, Projects: map[string]config.ProjectConfig{
		"example": {Root: root, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"},
	}})
	if _, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{Project: model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}, WriteOptions: WriteOptions{ExpectedHubRevision: hubHead}}); err != nil {
		t.Fatal(err)
	}
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
