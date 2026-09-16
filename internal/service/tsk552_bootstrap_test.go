package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK552ProjectOnboardDerivesCwdIdentityAndIsIdempotent(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	code := "RDX"
	result, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantID := filepath.Base(root)
	if result.ProjectID != wantID || result.ProjectCode != code || result.Status != "onboarded" {
		t.Fatalf("onboard result=%#v want project=%q", result, wantID)
	}
	entry, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	managed, ok := entry.Projects[wantID]
	if !ok || managed.Root != root || managed.ProjectCode != code {
		t.Fatalf("managed project=%#v found=%v", managed, ok)
	}
	managedBytes, err := os.ReadFile(config.ManagedProjectRegistryPath(s.Config.StateDir))
	if err != nil || bytes.Contains(managedBytes, []byte("airelay_session_key")) {
		t.Fatalf("managed project registry contains relay authority: err=%v data=%s", err, managedBytes)
	}
	effective, err := s.EffectiveProjectConfig(wantID)
	if err != nil || effective.AirelaySessionKey != "" {
		t.Fatalf("managed project invented relay authority=%q err=%v", effective.AirelaySessionKey, err)
	}
	if _, err := s.ProjectRead(context.Background(), wantID); err != nil {
		t.Fatal(err)
	}
	identifiers, err := s.ProjectIdentifiersRead(context.Background(), wantID)
	if err != nil || identifiers.ProjectCode != code {
		t.Fatalf("identifiers=%#v err=%v", identifiers, err)
	}
	retry, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	})
	if err != nil || retry.Status != "already_registered" {
		t.Fatalf("retry=%#v err=%v", retry, err)
	}
	resolved, err := s.ProjectIDForRoot(context.Background(), root)
	if err != nil || resolved != wantID {
		t.Fatalf("resolved project=%q err=%v", resolved, err)
	}
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: "BAD",
	}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("project code conflict err=%v", err)
	}
}

func TestTSK552ProjectOnboardRejectsConflictingRepositoryRoot(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, firstRoot, _ := testutil.RepoWithBareRemote(t)
	_, secondRoot, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, firstRoot, "remote", "set-head", "origin", "main")
	testutil.Git(t, secondRoot, "remote", "set-head", "origin", "main")
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        firstRoot,
		ProjectCode: "RDX",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        secondRoot,
		ProjectCode: "RDX",
	}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting repository root was accepted: %v", err)
	}
}

func TestTSK552ProjectOnboardFailsClosedWhenRemoteDefaultIsUnavailable(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	testutil.Git(t, root, "symbolic-ref", "-d", "refs/remotes/origin/HEAD")
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: "RDX",
	}); err == nil || !strings.Contains(err.Error(), "resolve origin default branch") {
		t.Fatalf("remote default discovery failure was not fail-closed: %v", err)
	}
}

func TestTSK552AgentBootstrapIsolatesSameAgentCodeAcrossProjects(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, firstRoot, _ := testutil.RepoWithBareRemote(t)
	_, secondRoot, _ := testutil.RepoWithBareRemote(t)
	firstRenamed := filepath.Join(filepath.Dir(firstRoot), "first-project")
	secondRenamed := filepath.Join(filepath.Dir(secondRoot), "second-project")
	if err := os.Rename(firstRoot, firstRenamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(secondRoot, secondRenamed); err != nil {
		t.Fatal(err)
	}
	firstRoot, secondRoot = firstRenamed, secondRenamed
	testutil.Git(t, firstRoot, "remote", "set-head", "origin", "main")
	testutil.Git(t, secondRoot, "remote", "set-head", "origin", "main")
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        firstRoot,
		ProjectCode: "ONE",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        secondRoot,
		ProjectCode: "TWO",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	s.ConfigPath = filepath.Join(t.TempDir(), "missing-config.json")
	firstID := filepath.Base(firstRoot)
	secondID := filepath.Base(secondRoot)
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: firstID,
		AgentID:   "SHARED-AGENT",
		Role:      session.RoleWorker,
		Relay:     "first_runtime",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: secondID,
		AgentID:   "SHARED-AGENT",
		Role:      session.RoleLead,
		Relay:     "second_runtime",
	}); err != nil {
		t.Fatal(err)
	}
	first, err := s.AgentRead(context.Background(), firstID, "SHARED-AGENT")
	if err != nil || first.WorkflowRole != session.RoleWorker {
		t.Fatalf("first isolated Agent=%#v err=%v", first, err)
	}
	second, err := s.AgentRead(context.Background(), secondID, "SHARED-AGENT")
	if err != nil || second.WorkflowRole != session.RoleLead {
		t.Fatalf("second isolated Agent=%#v err=%v", second, err)
	}
}

func TestTSK552AgentBootstrapRejectsPortableWorkflowRoleConflict(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: "RDX",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	s.ConfigPath = filepath.Join(t.TempDir(), "missing-config.json")
	projectID := filepath.Base(root)
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		AgentID:   "CONFLICTING-AGENT",
		Role:      session.RoleWorker,
		Relay:     "shared_runtime",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		AgentID:   "CONFLICTING-AGENT",
		Role:      session.RoleLead,
		Relay:     "shared_runtime",
	}); err == nil || !strings.Contains(err.Error(), "workflow role") {
		t.Fatalf("portable workflow role conflict was accepted: %v", err)
	}
}

func TestTSK552LegacyEmptyWorkflowRoleRejectsConflictingActiveSession(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: "RDX",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	s.ConfigPath = filepath.Join(t.TempDir(), "missing-config.json")
	projectID := filepath.Base(root)
	hubRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AgentRegister(context.Background(), AgentRegisterInput{
		ProjectID:    projectID,
		AgentID:      "LEGACY-AGENT",
		WorkflowRole: "",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err != nil {
		t.Fatal(err)
	}
	relay := "legacy_runtime"
	if err := s.persistAgentBinding(projectID, "LEGACY-AGENT", config.AgentBinding{SessionKey: relay}); err != nil {
		t.Fatal(err)
	}
	store := session.NewStoreWithDurability(db)
	created, err := store.Create(session.CreateInput{
		ProjectID: projectID, ProjectCode: "RDX", Role: session.RoleLead,
		SessionType: session.SessionTypeChatGPT, SessionRef: &relay,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		AgentID:   "LEGACY-AGENT",
		Role:      session.RoleWorker,
		Relay:     relay,
	}); err == nil || !strings.Contains(err.Error(), "active Session role") {
		t.Fatalf("legacy empty role overrode conflicting active Session: %v", err)
	}
	legacy, err := s.AgentRead(context.Background(), projectID, "LEGACY-AGENT")
	if err != nil || legacy.WorkflowRole != "" {
		t.Fatalf("legacy Agent was mutated after conflict: %#v err=%v", legacy, err)
	}
	active, err := store.Get(created.ID)
	if err != nil || active.Status != session.StatusActive || active.Role != session.RoleLead {
		t.Fatalf("active Session changed after conflict: %#v err=%v", active, err)
	}
}

func TestTSK552AgentBootstrapPreservesUnrelatedActiveSessionAcrossRefresh(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, firstRoot, _ := testutil.RepoWithBareRemote(t)
	_, secondRoot, _ := testutil.RepoWithBareRemote(t)
	firstRenamed := filepath.Join(filepath.Dir(firstRoot), "active-project")
	secondRenamed := filepath.Join(filepath.Dir(secondRoot), "bootstrap-project")
	if err := os.Rename(firstRoot, firstRenamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(secondRoot, secondRenamed); err != nil {
		t.Fatal(err)
	}
	firstRoot, secondRoot = firstRenamed, secondRenamed
	testutil.Git(t, firstRoot, "remote", "set-head", "origin", "main")
	testutil.Git(t, secondRoot, "remote", "set-head", "origin", "main")
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        firstRoot,
		ProjectCode: "ONE",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        secondRoot,
		ProjectCode: "TWO",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	s.ConfigPath = filepath.Join(t.TempDir(), "missing-config.json")
	firstID := filepath.Base(firstRoot)
	secondID := filepath.Base(secondRoot)
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: firstID,
		AgentID:   "ACTIVE-WORKER",
		Role:      session.RoleWorker,
		Relay:     "active_runtime",
	}); err != nil {
		t.Fatal(err)
	}
	store := session.NewStoreWithDurability(db)
	relay := "active_runtime"
	activeSession, err := store.Create(session.CreateInput{
		ProjectID: firstID, ProjectCode: "ONE", Role: session.RoleWorker,
		SessionType: session.SessionTypeChatGPT, SessionRef: &relay,
	})
	if err != nil {
		t.Fatal(err)
	}
	persistedConfig := s.Config
	persistedConfig.Controller.TunnelHealthListenAddr = "127.0.0.1:8876"
	persisted, err := json.Marshal(persistedConfig)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, persisted, 0o600); err != nil {
		t.Fatal(err)
	}
	s.ConfigPath = configPath
	s.EnableHostConfigRefresh()
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: secondID,
		AgentID:   "BOOTSTRAP-WORKER",
		Role:      session.RoleWorker,
		Relay:     "bootstrap_runtime",
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveRuntimeRoleSession(context.Background(), relay, session.RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProjectID != firstID || resolved.Session.ID != activeSession.ID || resolved.Session.Status != session.StatusActive {
		t.Fatalf("unrelated active Session was not preserved: %#v", resolved)
	}
}

func TestTSK552AgentBootstrapUsesCanonicalRoleCodesAndNoSessionCreation(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	if _, err := s.ProjectOnboard(context.Background(), ProjectOnboardInput{
		Root:        root,
		ProjectCode: "RDX",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	s.ConfigPath = filepath.Join(t.TempDir(), "missing-config.json")
	projectID := filepath.Base(root)
	worker, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		Role:      session.RoleWorker,
		Relay:     "repodex_master",
	})
	if err != nil {
		t.Fatal(err)
	}
	if worker.AgentID != "RDX-WORKER" || worker.Role != session.RoleWorker || worker.Status != "registered" {
		t.Fatalf("worker=%#v", worker)
	}
	portableWorker, err := s.AgentRead(context.Background(), projectID, worker.AgentID)
	if err != nil || portableWorker.WorkflowRole != session.RoleWorker {
		t.Fatalf("portable Worker role=%q err=%v", portableWorker.WorkflowRole, err)
	}
	lead, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		Role:      session.RoleLead,
		Relay:     "repodex_lead",
	})
	if err != nil {
		t.Fatal(err)
	}
	if lead.AgentID != "RDX-LEAD" || lead.Role != session.RoleLead {
		t.Fatalf("lead=%#v", lead)
	}
	portableLead, err := s.AgentRead(context.Background(), projectID, lead.AgentID)
	if err != nil || portableLead.WorkflowRole != session.RoleLead {
		t.Fatalf("portable Lead role=%q err=%v", portableLead.WorkflowRole, err)
	}
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		AgentID:   "RDX-WORKER",
		Role:      session.RoleLead,
		Relay:     "repodex_master",
	}); err == nil || !strings.Contains(err.Error(), "workflow role") {
		t.Fatalf("workflow role conflict err=%v", err)
	}
	retry, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		Role:      session.RoleWorker,
		Relay:     "repodex_master",
	})
	if err != nil || retry.Status != "already_registered" {
		t.Fatalf("retry=%#v err=%v", retry, err)
	}
	explicit, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		AgentID:   "RDX-LEAD-ALT",
		Role:      session.RoleLead,
		Relay:     "repodex_lead_alt",
	})
	if err != nil || explicit.AgentID != "RDX-LEAD-ALT" {
		t.Fatalf("explicit Agent code=%#v err=%v", explicit, err)
	}
	if _, err := s.AgentBootstrap(context.Background(), AgentBootstrapInput{
		ProjectID: projectID,
		Role:      session.RoleWorker,
		Relay:     "other_runtime",
	}); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("relay conflict err=%v", err)
	}
	records, err := session.NewStoreWithDurability(db).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("Agent bootstrap created durable Sessions: %#v", records)
	}
	if _, err := os.Stat(config.ManagedProjectRegistryPath(s.Config.StateDir)); err != nil {
		t.Fatal(err)
	}
}
