package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	workflowSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

// tsk692Service builds a Gateway service bound to one Hub repository but an
// otherwise empty Local state directory, mirroring a fresh machine.
func tsk692Service(t *testing.T, dir, hubBare string) (*Service, *sqlitestore.Databases) {
	t.Helper()
	airelay := filepath.Join(dir, "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) if [ \"$3\" = --json ]; then printf '{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"; else printf 'Controller: reachable\\nState: idle\\n'; fi ;;\ntail) printf 'idle\\n' ;;\n*) exit 99 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	s := New(config.Config{
		SchemaVersion: 1, GatewayID: "HOM", ListenAddr: "127.0.0.1:8875",
		StateDir: stateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20,
		MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: airelay,
		Hub:      config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
		Projects: map[string]config.ProjectConfig{},
	})
	s.Config.Controller = config.ControllerConfig{
		GatewayBinary:          "/nonexistent/gpt-tunnel-gatewayd",
		TunnelClientBinary:     "/nonexistent/tunnel-client",
		TunnelEnvFile:          filepath.Join(dir, "tunnel.env"),
		PIDDir:                 filepath.Join(dir, "pids"),
		LogDir:                 filepath.Join(dir, "logs"),
		TunnelHealthListenAddr: "127.0.0.1:9889",
	}
	s.ConfigPath = filepath.Join(dir, "config.json")
	if err := fsutil.WriteJSONAtomic(s.ConfigPath, s.Config, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	if err := s.Hub.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, db
}

// tsk692SeedPortableSemantics writes representative portable Hub entities for
// the onboarded project so the adopt regression proves family restore.
func tsk692SeedPortableSemantics(t *testing.T, s *Service, projectID, code string) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	task, err := model.NewTask(projectID, code+"-TSK1", model.AuthoringDraft{
		Title: "Restored Task", Summary: "portable summary", Objective: "portable objective", ADRRelation: model.TaskADRNoRequired,
	}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	milestone := model.Milestone{
		SchemaVersion: model.MilestoneSchemaVersion, ID: code + "-MIL1", ProjectID: projectID, Revision: 1,
		Title: "Restored Milestone", Status: model.MilestoneActive, Tasks: []string{task.ID},
		CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now,
	}
	track := model.Track{
		SchemaVersion: model.TrackSchemaVersion, ID: code + "-TRK1", ProjectID: projectID, Revision: 1,
		Milestone: milestone.ID, Title: "Restored Track", Tasks: []string{task.ID}, Status: model.TrackActive,
		CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now,
	}
	relation := model.Relation{
		SchemaVersion: model.RelationSchemaVersion, ProjectID: projectID, Kind: model.RelationKindCorrects,
		Source: task.ID, Target: code + "-TSK9", CreatedAt: now, CreatedBy: "planner",
	}
	journal := model.JournalEntry{
		SchemaVersion: model.SchemaVersion, ID: code + "-JRN1", ProjectID: projectID, Status: model.JournalStatusPublished,
		Stream: model.JournalStreamPlannerNotes, Actor: "planner", Role: "planner", SessionID: "HOM_PLNR_x", Sequence: 1, CreatedAt: now,
		Data: json.RawMessage(`{"summary":"portable note","decisions":[],"commitments":[],"facts":[],"assumptions":[],"blockers":[],"unresolved":[],"next_actions":[],"references":[]}`),
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	revision := portableSharedRevision{
		EntityType:    "task",
		EntityID:      task.ID,
		ProjectID:     projectID,
		Revision:      1,
		MutationKind:  "create",
		Actor:         "planner",
		Reason:        "created",
		ChangedFields: []string{"title"},
		Payload:       taskPayload,
		RecordedAt:    now.Format(time.RFC3339Nano),
	}
	lifecycle := portableSharedLifecycleEvent{
		OperationID:   code + "-OPR7",
		EntityType:    "milestone",
		ProjectID:     projectID,
		EntityID:      milestone.ID,
		Revision:      1,
		EventKind:     "status",
		MutationKind:  "status",
		FromStatus:    model.MilestonePlanned,
		ToStatus:      model.MilestoneActive,
		Actor:         "planner",
		Reason:        "activated",
		Contract:      json.RawMessage(`{"schema_version":1,"reason":"activated"}`),
		ChangedFields: []string{"status"},
		RecordedAt:    now.Format(time.RFC3339Nano),
	}
	items := []struct {
		path  string
		value any
	}{
		{s.taskAuthoringPath(projectID, task.ID), task},
		{s.milestonePath(projectID, milestone.ID), milestone},
		{s.trackPath(projectID, track.ID), track},
		{s.relationPath(relation), relation},
		{s.journalPath(projectID, journal.ID), journal},
		{s.sharedRevisionPath(projectID, "task", task.ID, 1), revision},
		{s.sharedLifecycleEventPath(projectID, "milestone", milestone.ID, lifecycle.OperationID), lifecycle},
	}
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.path)
	}
	if _, err := s.Hub.Transact(ctx, "", "seed portable adopt fixture", func(worktree string) ([]string, error) {
		for _, item := range items {
			if err := hub.WriteJSON(worktree, item.path, item.value); err != nil {
				return nil, err
			}
		}
		return paths, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func tsk692RegistryHasProject(t *testing.T, stateDir, projectID string) bool {
	t.Helper()
	registry, err := config.LoadManagedProjects(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := registry.Projects[projectID]
	return ok
}

func tsk692SharedCount(t *testing.T, db *sqlitestore.Databases, sql string, args ...any) int {
	t.Helper()
	rows, err := db.Shared.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows.Rows)
}

func TestTSK692ProjectOnboardAdoptsHubKnownProjectFromEmptyLocal(t *testing.T) {
	hubBare, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	ctx := context.Background()
	projectID := filepath.Base(root)
	code := "ADP"

	source, _ := tsk692Service(t, t.TempDir(), hubBare)
	first, err := source.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
		WorkerRelay: "adp_source_worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "onboarded" {
		t.Fatalf("new-project onboard status=%#v", first)
	}
	tsk692SeedPortableSemantics(t, source, projectID, code)

	fresh, freshDB := tsk692Service(t, t.TempDir(), hubBare)
	adopted, err := fresh.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
		WorkerRelay: "adp_fresh_worker",
		LeadRelay:   "adp_fresh_lead",
	})
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Status != "adopted" {
		t.Fatalf("adopt status=%#v", adopted)
	}
	if adopted.Token == "" || len(adopted.Agents) != 2 {
		t.Fatalf("adopt result=%#v", adopted)
	}
	if !tsk692RegistryHasProject(t, fresh.Config.StateDir, projectID) {
		t.Fatal("fresh Local managed registry does not contain the adopted project")
	}
	grant, err := freshDB.ReadSessionBootstrapGrant(ctx, projectID)
	if err != nil || grant.ProjectCode != code || grant.Role != workflowSession.RolePlanner || grant.Token != adopted.Token {
		t.Fatalf("planner bootstrap grant=%#v err=%v", grant, err)
	}
	for _, check := range []struct {
		name string
		sql  string
		want int
	}{
		{"task", "SELECT id FROM shared_tasks WHERE id=?", 1},
		{"milestone", "SELECT id FROM shared_milestones WHERE id=?", 1},
		{"track", "SELECT id FROM shared_tracks WHERE id=?", 1},
		{"journal", "SELECT id FROM shared_journals WHERE id=?", 1},
	} {
		entityID := map[string]string{"task": code + "-TSK1", "milestone": code + "-MIL1", "track": code + "-TRK1", "journal": code + "-JRN1"}[check.name]
		if got := tsk692SharedCount(t, freshDB, check.sql, entityID); got != check.want {
			t.Fatalf("fresh Shared %s rows=%d want %d", check.name, got, check.want)
		}
	}
	if got := tsk692SharedCount(t, freshDB, "SELECT source_id FROM shared_relations WHERE project_id=?", projectID); got != 1 {
		t.Fatalf("fresh Shared relation rows=%d want 1", got)
	}
	if got := tsk692SharedCount(t, freshDB, "SELECT project_id FROM shared_entity_revisions WHERE project_id=?", projectID); got < 1 {
		t.Fatalf("fresh Shared revision evidence rows=%d want >=1", got)
	}
	identifiers, err := freshDB.ReadSharedProjectIdentifiers(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if identifiers.ProjectCode != code || identifiers.NextTaskNumber < 2 {
		t.Fatalf("restored identifiers=%#v", identifiers)
	}
	events, err := freshDB.Shared.Query(ctx, "SELECT id FROM shared_lifecycle_events WHERE project_id=?", projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Rows) != 1 {
		t.Fatalf("restored lifecycle events=%#v", events.Rows)
	}
	sessions, err := workflowSession.NewStoreWithDurability(freshDB).List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range sessions {
		if record.ProjectID == projectID {
			t.Fatalf("source-machine Session leaked into fresh Local state: %s", record.ID)
		}
	}
	agents, err := fresh.AgentList(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Fatalf("fresh Agent attachments=%#v", agents)
	}
	repeat, err := fresh.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
		WorkerRelay: "adp_fresh_worker",
		LeadRelay:   "adp_fresh_lead",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Status != "already_registered" {
		t.Fatalf("repeat onboard status=%#v", repeat)
	}
}

func TestTSK692ProjectOnboardAdoptFailsClosedOnIdentityConflict(t *testing.T) {
	hubBare, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	ctx := context.Background()
	projectID := filepath.Base(root)
	code := "CNF"

	source, _ := tsk692Service(t, t.TempDir(), hubBare)
	if _, err := source.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	}); err != nil {
		t.Fatal(err)
	}

	fresh, _ := tsk692Service(t, t.TempDir(), hubBare)
	if _, err := fresh.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: "ZZZ",
	}); err == nil {
		t.Fatal("adopt with conflicting project code succeeded")
	}
	if tsk692RegistryHasProject(t, fresh.Config.StateDir, projectID) {
		t.Fatal("conflicting adopt published a managed registry entry")
	}

	secondDir := t.TempDir()
	second, _ := tsk692Service(t, secondDir, hubBare)
	if _, err := second.Hub.Transact(ctx, "", "test: conflict repository identity", func(worktree string) ([]string, error) {
		var project model.Project
		if err := readWorktreeJSON(worktree, second.projectPath(projectID), &project); err != nil {
			return nil, err
		}
		project.RepositoryURL = "git@example.invalid:other.git"
		if err := hub.WriteJSON(worktree, second.projectPath(projectID), project); err != nil {
			return nil, err
		}
		return []string{second.projectPath(projectID)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: code,
	}); err == nil {
		t.Fatal("adopt with conflicting repository identity succeeded")
	}
	if tsk692RegistryHasProject(t, second.Config.StateDir, projectID) {
		t.Fatal("conflicting adopt published a managed registry entry")
	}
}
