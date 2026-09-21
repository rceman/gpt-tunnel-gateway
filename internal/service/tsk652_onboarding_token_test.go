package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/agentguide"
	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK652ProjectOnboardReturnsStablePlannerBootstrapToken(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	ctx := context.Background()
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	projectID := filepath.Base(root)
	first, err := s.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: "AIR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "onboarded" || first.Token == "" || first.TokenUsage != ProjectOnboardTokenUsage {
		t.Fatal("fresh onboarding did not return the expected token contract")
	}
	grant, err := db.ReadSessionBootstrapGrant(ctx, projectID)
	if err != nil || grant.Token != first.Token || grant.Role != durableSession.RolePlanner {
		t.Fatalf("bootstrap grant did not match the onboarding result: err=%v", err)
	}
	retry, err := s.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: "AIR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Status != "already_registered" || retry.Token != first.Token || retry.TokenUsage != ProjectOnboardTokenUsage {
		t.Fatal("repeat onboarding did not return the stable token contract")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s.Durability = reopened
	identityRemote := testutil.Git(t, root, "remote", "get-url", "origin")
	retrieved, err := s.ProjectToken(ctx, ProjectTokenInput{
		Root:   root,
		Remote: identityRemote,
	})
	if err != nil || retrieved.Token != first.Token {
		t.Fatalf("restart token retrieval failed or changed: err=%v", err)
	}
	resolution, err := s.ResolveSessionBootstrapToken(ctx, first.Token)
	if err != nil || resolution.Grant.ProjectID != projectID || resolution.Grant.Role != durableSession.RolePlanner {
		t.Fatalf("token resolution did not select the onboarded Planner grant: err=%v", err)
	}
	_, isolatedRoot, _ := testutil.RepoWithBareRemote(t)
	isolatedRoot, err = filepath.Abs(isolatedRoot)
	if err != nil {
		t.Fatal(err)
	}
	isolatedTarget := filepath.Join(filepath.Dir(isolatedRoot), "isolated-project")
	if err := os.Rename(isolatedRoot, isolatedTarget); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, isolatedTarget, "remote", "set-head", "origin", "main")
	isolated, err := s.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        isolatedTarget,
		ProjectCode: "ISO",
	})
	if err != nil || isolated.Token == "" || isolated.Token == first.Token {
		t.Fatalf("isolated project onboarding did not create a distinct grant: err=%v", err)
	}
	isolatedResolution, err := s.ResolveSessionBootstrapToken(ctx, isolated.Token)
	if err != nil || isolatedResolution.Grant.ProjectID != filepath.Base(isolatedTarget) {
		t.Fatalf("isolated token resolved to the wrong project: err=%v", err)
	}
	started, err := s.SessionStart(authority.WithPlanner(ctx), SessionStartInput{
		ProjectID:   projectID,
		ProjectCode: "AIR",
		Role:        durableSession.RolePlanner,
		SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil || started.Session.ProjectID != projectID || started.Session.Role != durableSession.RolePlanner {
		t.Fatalf("session start=%#v err=%v", started, err)
	}
	bound := WithAgentSessionID(ctx, started.Session.ID)
	surfaces := make([]any, 0, 8)
	projects, err := s.ProjectList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.ProjectRead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectOperationalStatus(bound)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := s.SessionList(bound)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.SessionInfo(bound, started.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := runtime_log.New(s.Config.StateDir).Read(runtime_log.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	surfaces = append(surfaces, projects, project, status, sessions, info, logs, agentguide.Canonical())
	encoded, err := json.Marshal(surfaces)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), first.Token) {
		t.Fatal("project bootstrap token leaked outside onboarding and authenticated project token retrieval")
	}
}
