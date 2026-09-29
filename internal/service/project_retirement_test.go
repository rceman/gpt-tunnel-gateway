package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestDebugProjectRetirementTransitionExcludesStaleProjectsAndPreservesGateway(t *testing.T) {
	ctx := context.Background()
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	s.Config.Debug.Enabled = true
	db, err := sqlitestore.OpenWithObserverDeferredProjectConfigurationMigration(s.Config.StateDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	s.localState = db
	if _, err := s.DebugRetireProject(ctx, "gpt-tunnel-gateway", "must remain active"); !errors.Is(err, sqlitestore.ErrProjectRetirementUnsafe) {
		t.Fatalf("retirement accepted the active Gateway project: %v", err)
	}
	if _, found, err := db.ReadSharedProjectRetirement(ctx, "gpt-tunnel-gateway"); err != nil || found {
		t.Fatalf("active Gateway retirement attempt wrote a tombstone: found=%v err=%v", found, err)
	}

	projectIDs := []string{"agentir", "reposuite-mcp", "gpt-tunnel-gateway"}
	registry := config.EmptyManagedProjectRegistry()
	registry.Revision = 1
	registry.Projects = make(map[string]config.ManagedProjectEntry, len(projectIDs))
	projectCodes := map[string]string{"agentir": "AIR", "reposuite-mcp": "RSM", "gpt-tunnel-gateway": "GTW"}
	for _, projectID := range projectIDs {
		registry.Projects[projectID] = config.ManagedProjectEntry{
			Root: t.TempDir(), RepositoryURL: "https://example.invalid/" + projectID + ".git", Remote: "origin",
			DefaultBranch: "main", ProjectCode: projectCodes[projectID],
		}
	}
	currentRegistry, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	expectedDigest, err := currentRegistry.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteManagedProjectRegistry(s.Config.StateDir, expectedDigest, registry); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	activeProject, activeConfiguration := retirementHubProject(t, "gpt-tunnel-gateway", now)
	for _, projectID := range projectIDs[:2] {
		project, configuration := retirementHubProject(t, projectID, now)
		if err := writeRetirementHubProject(ctx, s, project, configuration); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeRetirementHubProject(ctx, s, activeProject, activeConfiguration); err != nil {
		t.Fatal(err)
	}
	activePayload, err := json.Marshal(activeConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: activeConfiguration.ProjectID, Revision: int64(activeConfiguration.Revision), Payload: activePayload,
		UpdatedAt: activeConfiguration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	activeHubConfiguration, err := s.Hub.ReadFile(ctx, s.projectConfigurationPath(activeConfiguration.ProjectID))
	if err != nil {
		t.Fatal(err)
	}

	var staleConfigEntry sqlitestore.OutboxEntry
	for _, projectID := range projectIDs[:2] {
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, projectID, 1, retirementV2Payload(t, projectID, 1), now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		for revision := int64(2); revision <= 3; revision++ {
			id := projectID + "-outbox-" + strconv.FormatInt(revision, 10)
			payload := retirementV2Payload(t, projectID, revision)
			if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, id, "project_configuration", projectID, projectID, revision, "project-configuration-update", payload, now.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			if projectID == "agentir" && revision == 3 {
				var found bool
				staleConfigEntry, found, err = db.ReadSharedOutboxEntry(ctx, id)
				if err != nil || !found {
					t.Fatalf("read pending configuration publication: found=%v err=%v", found, err)
				}
			}
		}
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", projectID, projectID, 1, "update", "planner", "preserved stale history", []byte(`[]`), []byte(`not-json`), now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	beforeIDs, err := s.EffectiveProjectIDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, projectID := range projectIDs {
		if !retirementContainsProject(beforeIDs, projectID) {
			t.Fatalf("project %q missing from active registry before retirement: %#v", projectID, beforeIDs)
		}
	}
	for _, projectID := range projectIDs[:2] {
		result, err := s.DebugRetireProject(ctx, projectID, "stale transition project")
		if err != nil || result.Status != "retired" || result.ConfigurationRevision != 3 || result.CancelledPublications != 2 || result.AlreadyRetired {
			t.Fatalf("retire %s: result=%#v err=%v", projectID, result, err)
		}
		replayed, err := s.DebugRetireProject(ctx, projectID, "stale transition project")
		if err != nil || !replayed.AlreadyRetired {
			t.Fatalf("idempotent retire %s: result=%#v err=%v", projectID, replayed, err)
		}
		if _, err := s.DebugRetireProject(ctx, projectID, "conflicting retirement reason"); !errors.Is(err, sqlitestore.ErrProjectRetirementConflict) {
			t.Fatalf("conflicting retirement reason err=%v", err)
		}
		if _, err := s.ProjectConfigurationRead(ctx, projectID); err == nil {
			t.Fatalf("retired project %q remained readable", projectID)
		}
		if data, err := s.Hub.ReadFile(ctx, s.projectConfigurationPath(projectID)); err == nil || !IsNotFound(err) {
			t.Fatalf("retired Hub configuration %q remains: err=%v data=%s", projectID, err, data)
		}
		if data, err := s.Hub.ReadFile(ctx, s.projectIdentifiersPath(projectID)); err == nil || !IsNotFound(err) {
			t.Fatalf("retired Hub identifiers %q remain: err=%v data=%s", projectID, err, data)
		}
		retirementData, err := s.Hub.ReadFile(ctx, s.projectRetirementPath(projectID))
		if err != nil {
			t.Fatalf("portable Hub retirement marker %q missing: %v", projectID, err)
		}
		var retirement model.ProjectRetirement
		if err := decodeStrict(retirementData, &retirement); err != nil || model.ValidateProjectRetirement(retirement) != nil || retirement.ProjectID != projectID {
			t.Fatalf("portable Hub retirement marker %q invalid: record=%#v err=%v", projectID, retirement, err)
		}
		rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_project_configurations WHERE id=?`, projectID)
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(0) {
			t.Fatalf("retired Shared configuration %q remains active: rows=%#v err=%v", projectID, rows, err)
		}
		outbox, err := db.Shared.Query(ctx, `SELECT COUNT(cancelled_at),COUNT(cancellation_reason) FROM hub_outbox WHERE entity_type='project_configuration' AND entity_id=?`, projectID)
		if err != nil || len(outbox.Rows) != 1 || outbox.Rows[0][0] != int64(2) || outbox.Rows[0][1] != int64(2) {
			t.Fatalf("retired outbox %q lacks cancellation evidence: rows=%#v err=%v", projectID, outbox, err)
		}
		history, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=?`, projectID)
		if err != nil || len(history.Rows) != 1 || history.Rows[0][0] != int64(1) {
			t.Fatalf("retirement deleted %q configuration history: rows=%#v err=%v", projectID, history, err)
		}
	}
	if err := s.deliverSharedOutboxEntry(ctx, staleConfigEntry); err != nil {
		t.Fatalf("stale in-flight ProjectConfiguration publication was not cancelled: %v", err)
	}

	afterIDs, err := s.EffectiveProjectIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !retirementContainsProject(afterIDs, "gpt-tunnel-gateway") {
		t.Fatalf("active Gateway disappeared from the effective project registry: %#v", afterIDs)
	}
	for _, projectID := range projectIDs[:2] {
		if retirementContainsProject(afterIDs, projectID) {
			t.Fatalf("retired project %q remains in the effective registry: %#v", projectID, afterIDs)
		}
	}
	registry, err = config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Projects["gpt-tunnel-gateway"]; !ok || len(registry.Projects) != 1 {
		t.Fatalf("retirement removed the active project or retained stale entries: %#v", registry)
	}
	activeAfter, err := s.Hub.ReadFile(ctx, s.projectConfigurationPath("gpt-tunnel-gateway"))
	if err != nil || string(activeHubConfiguration) != string(activeAfter) {
		t.Fatalf("active Gateway Hub configuration changed: err=%v", err)
	}
	if _, err := db.ReadSharedEntity(ctx, "project_configuration", "gpt-tunnel-gateway"); err != nil {
		t.Fatalf("active Gateway Shared configuration was removed: %v", err)
	}

	if err := s.DebugMigrateProjectConfigurations(ctx); err != nil {
		t.Fatalf("deferred migration after retirements: %v", err)
	}
	complete, err := db.ProjectConfigurationMigrationComplete(ctx)
	if err != nil || !complete {
		t.Fatalf("migration complete=%v err=%v", complete, err)
	}

	staleProject, staleConfiguration := retirementHubProject(t, "agentir", now)
	if err := writeRetirementHubProject(ctx, s, staleProject, staleConfiguration); err != nil {
		t.Fatal(err)
	}
	staleProjectPath := s.projectPath("agentir")
	staleConfigurationPath := s.projectConfigurationPath("agentir")
	staleTaskPath := s.taskAuthoringPath("agentir", "AIR-TSK1")
	hubRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(ctx, hubRevision, "test: place stale state in retired Hub history", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, staleProjectPath, staleProject); err != nil {
			return nil, err
		}
		if err := hub.WriteText(worktree, staleConfigurationPath, "not-json"); err != nil {
			return nil, err
		}
		if err := hub.WriteText(worktree, staleTaskPath, "not-json"); err != nil {
			return nil, err
		}
		return []string{staleProjectPath, staleConfigurationPath, staleTaskPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreHubProjectSemantics(ctx, "agentir", "AIR"); err != nil {
		t.Fatalf("retired Hub snapshot blocked restore with an error: %v", err)
	}
	if _, err := db.ReadSharedEntity(ctx, "task", "AIR-TSK1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Hub restore resurrected retired project Task: %v", err)
	}

	freshConfig := s.Config
	freshConfig.StateDir = t.TempDir()
	freshDB, err := sqlitestore.OpenWithObserverDeferredProjectConfigurationMigration(freshConfig.StateDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = freshDB.Close() })
	fresh := NewWithDurabilityDeferredWorkers(freshConfig, freshDB)
	if err := fresh.Hub.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	projects, err := fresh.ProjectList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		if project.ID == "agentir" {
			t.Fatal("Hub-only retirement marker allowed a stale project listing")
		}
	}
	if _, err := fresh.ProjectRead(ctx, "agentir"); err == nil {
		t.Fatal("Hub-only retirement marker allowed stale project read")
	}
	if _, err := fresh.ProjectIdentifiersRead(ctx, "agentir"); err == nil {
		t.Fatal("Hub-only retirement marker allowed stale project identifiers read")
	}
	if _, err := fresh.ProjectConfigurationRead(ctx, "agentir"); err == nil {
		t.Fatal("Hub-only retirement marker allowed stale ProjectConfiguration read")
	}
	if _, err := fresh.ProjectWorkflowPolicyRead(ctx, "agentir"); err == nil {
		t.Fatal("Hub-only retirement marker allowed stale workflow policy read")
	}
	_, staleConfiguration = retirementHubProject(t, "agentir", time.Now().UTC())
	stalePayload, err := json.Marshal(staleConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	if err := freshDB.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: staleConfiguration.ProjectID, Revision: int64(staleConfiguration.Revision), Payload: stalePayload,
		UpdatedAt: staleConfiguration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	projectConfigs := make(map[string]config.ProjectConfig, len(fresh.Config.Projects)+1)
	for id, project := range fresh.Config.Projects {
		projectConfigs[id] = project
	}
	projectConfigs["agentir"] = config.ProjectConfig{
		Root: t.TempDir(), Mirror: t.TempDir(), Remote: "origin", DefaultBranch: "main", ProjectCode: "AIR", AirelaySessionKey: "agentir_master",
	}
	fresh.Config.Projects = projectConfigs
	if _, err := fresh.ProjectConfigurationRead(ctx, "agentir"); err == nil {
		t.Fatal("Hub-only retirement marker allowed stale Shared configuration read")
	}
	if _, err := fresh.SessionStart(authority.WithPlanner(ctx), SessionStartInput{
		ProjectID:   "agentir",
		Role:        durableSession.RolePlanner,
		SessionType: durableSession.SessionTypeChatGPT,
	}); err == nil {
		t.Fatal("Hub-only retirement marker allowed new Session admission")
	}
	if err := fresh.MigrateHubProjectConfigurations(ctx); err != nil {
		t.Fatalf("Hub migration scanned a Hub-retired ProjectConfiguration: %v", err)
	}
	if err := fresh.restoreHubProjectSemantics(ctx, "agentir", "AIR"); err != nil {
		t.Fatalf("Hub-only retirement marker allowed semantic restore: %v", err)
	}
	if _, err := freshDB.ReadSharedEntity(ctx, "task", "AIR-TSK1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Hub-only restore resurrected retired Task: %v", err)
	}
	if err := fresh.SyncProjectRetirements(ctx); err != nil {
		t.Fatalf("fresh-machine retirement import: %v", err)
	}
	if err := fresh.restoreHubProjectSemantics(ctx, "agentir", "AIR"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := freshDB.ReadSharedProjectRetirement(ctx, "agentir"); err != nil || !found {
		t.Fatalf("fresh machine did not restore retirement tombstone: found=%v err=%v", found, err)
	}
	if err := fresh.SyncProjectRetirements(ctx); err != nil {
		t.Fatalf("idempotent fresh-machine retirement sync: %v", err)
	}
	if err := fresh.DebugMigrateProjectConfigurations(ctx); err != nil {
		t.Fatalf("fresh-machine configuration migration after Hub retirement import: %v", err)
	}
	if complete, err := freshDB.ProjectConfigurationMigrationComplete(ctx); err != nil || !complete {
		t.Fatalf("fresh-machine ProjectConfiguration migration complete=%v err=%v", complete, err)
	}
	if _, err := freshDB.ReadSharedEntity(ctx, "task", "AIR-TSK1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh restore resurrected retired Task: %v", err)
	}
	projects, err = fresh.ProjectList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, project := range projects {
		listed[project.ID] = true
	}
	if !listed["gpt-tunnel-gateway"] || listed["agentir"] || listed["reposuite-mcp"] {
		t.Fatalf("fresh Hub restore project listing=%#v", listed)
	}
}

func retirementHubProject(t *testing.T, projectID string, now time.Time) (model.Project, model.ProjectConfiguration) {
	t.Helper()
	project := model.Project{
		SchemaVersion: 1, ID: projectID, RepositoryURL: "https://example.invalid/" + projectID + ".git", DefaultBranch: "main",
		WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "b1a45b1e9475ab29dfd3e84d523b70897c7b8918", Status: "active",
	}
	configuration := model.DefaultProjectConfiguration(projectID, now)
	return project, configuration
}

func writeRetirementHubProject(ctx context.Context, s *Service, project model.Project, configuration model.ProjectConfiguration) error {
	projectCode := map[string]string{"agentir": "AIR", "reposuite-mcp": "RSM", "gpt-tunnel-gateway": "GTW"}[project.ID]
	identifiers := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: project.ID, ProjectCode: projectCode, NextTaskNumber: 1, NextADRNumber: 1}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return err
	}
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		return err
	}
	paths := []string{s.projectPath(project.ID), s.projectIdentifiersPath(project.ID), s.projectConfigurationPath(project.ID)}
	_, err = s.Hub.Transact(ctx, revision, "test: seed retirement project "+project.ID, func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, paths[0], project); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[1], identifiers); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[2], configuration); err != nil {
			return nil, err
		}
		return paths, nil
	})
	return err
}

func retirementV2Payload(t *testing.T, projectID string, revision int64) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"schema_version": 2, "project_id": projectID, "revision": revision, "workflow": map[string]any{"retired": true}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func retirementContainsProject(projects []string, projectID string) bool {
	for _, candidate := range projects {
		if candidate == projectID {
			return true
		}
	}
	return false
}
