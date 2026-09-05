package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestServiceSessionLifecycleUsesRegisteredProject(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	hubBare, root, hubHead := testutil.RepoWithBareRemote(t)
	c := config.Config{StateDir: state, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"}, Projects: map[string]config.ProjectConfig{
		"example": {Root: root, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_master"},
	}}
	db, err := sqlitestore.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewWithDurabilityDeferredWorkers(c, db)
	if _, err := s.ProjectRegister(context.Background(), ProjectRegisterInput{
		Project: model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubHead,
		},
	}); err != nil {
		t.Fatal(err)
	}
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ProjectIdentifiersAdopt(authority.WithPlanner(context.Background()), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err != nil {
		t.Fatal(err)
	}
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload,
		UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	started, err := s.SessionStart(authority.WithPlanner(context.Background()), SessionStartInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		Role:        durableSession.RolePlanner,
		SessionType: "chatgpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Session.ProjectID != "example" || started.Session.Role != durableSession.RolePlanner || started.Session.Status != "active" {
		t.Fatalf("start result=%#v", started)
	}
	info, err := s.SessionInfo(context.Background(), started.Session.ID)
	if err != nil || info.Session.ID != started.Session.ID {
		t.Fatalf("info result=%#v err=%v", info, err)
	}
	if _, err := s.SessionStart(authority.WithPlanner(context.Background()), SessionStartInput{
		ProjectID:   "missing",
		Role:        durableSession.RolePlanner,
		SessionType: "chatgpt",
	}); err == nil {
		t.Fatal("unknown project session start accepted")
	}
}
