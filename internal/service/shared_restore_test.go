package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestPortableHubRestoreHydratesTrackRelationsAndSequences(t *testing.T) {
	s, _, _ := testServiceSerial(t)
	db := testServiceWithDurability(t, s)
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	task, err := model.NewTask("example", "EXM-TSK12", model.AuthoringDraft{
		Title: "Restored Task", Summary: "portable summary", Objective: "portable objective", ADRRelation: model.TaskADRNoRequired,
	}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	milestone := model.Milestone{
		SchemaVersion: model.MilestoneSchemaVersion, ID: "EXM-MIL4", ProjectID: "example", Revision: 2,
		Title: "Restored Milestone", Status: model.MilestoneActive, Tasks: []string{task.ID},
		CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now,
	}
	track := model.Track{
		SchemaVersion: model.TrackSchemaVersion, ID: "EXM-TRK6", ProjectID: "example", Revision: 3,
		Milestone: milestone.ID, Title: "Restored Track", Tasks: []string{task.ID}, Status: model.TrackActive,
		CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now,
	}
	relation := model.Relation{
		SchemaVersion: model.RelationSchemaVersion, ProjectID: "example", Kind: model.RelationKindCorrects,
		Source: "EXM-TSK12", Target: "EXM-TSK13", CreatedAt: now, CreatedBy: "planner",
	}
	adr := model.ADR{
		SchemaVersion: model.SchemaVersion, ID: "EXM-ADR8", ProjectID: "example", Revision: 2,
		Title: "Restored decision", Summary: "portable decision summary", Status: model.ADRStatusAccepted,
		Context: "Context", Decision: "Decision", Consequences: "Consequences",
		CreatedAt: now, CreatedBy: "planner", UpdatedAt: now.Add(time.Second), UpdatedBy: "planner",
	}
	lifecycle := portableSharedLifecycleEvent{
		OperationID:   "EXM-OPR91",
		EntityType:    "adr",
		ProjectID:     "example",
		EntityID:      adr.ID,
		Revision:      2,
		EventKind:     "status",
		MutationKind:  "status",
		FromStatus:    model.ADRStatusProposed,
		ToStatus:      model.ADRStatusAccepted,
		Actor:         "planner",
		Reason:        "accepted",
		Contract:      json.RawMessage(`{"schema_version":1,"reason":"accepted"}`),
		ChangedFields: []string{"status"},
		RecordedAt:    now.Add(time.Second).Format(time.RFC3339Nano),
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	history := portableSharedRevision{
		EntityType:    "task",
		EntityID:      task.ID,
		ProjectID:     task.ProjectID,
		Revision:      1,
		MutationKind:  "create",
		Actor:         "planner",
		Reason:        "created",
		ChangedFields: []string{"title"},
		Payload:       taskPayload,
		RecordedAt:    now.Format(time.RFC3339Nano),
	}
	historyPath := s.sharedRevisionPath("example", "task", task.ID, 1)
	lifecyclePath := s.sharedLifecycleEventPath("example", "adr", adr.ID, lifecycle.OperationID)
	counterPath := s.projectPrefix("example") + "/operator-journal/counter.json"
	counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: "example", NextEventNumber: 22}
	legacyAgent := model.Agent{
		SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "legacy-coder", Role: model.AgentRoleCoding,
		Enabled: true, RecommendedReasoning: model.ReasoningHigh, Capabilities: []string{"git", "review"}, CreatedAt: now, UpdatedAt: now,
	}
	legacyAgentPath := s.projectPrefix("example") + "/agents/legacy-coder.json"
	paths := []string{
		s.taskAuthoringPath("example", task.ID), s.milestonePath("example", milestone.ID),
		s.trackPath("example", track.ID), s.relationPath(relation), historyPath,
		s.adrPath("example", adr.ID), lifecyclePath, counterPath, legacyAgentPath,
	}
	if _, err := s.Hub.Transact(ctx, "", "seed portable restore fixture", func(worktree string) ([]string, error) {
		for _, item := range []struct {
			path  string
			value any
		}{{paths[0], task}, {paths[1], milestone}, {paths[2], track}, {paths[3], relation}, {paths[4], history}, {paths[5], adr}, {paths[6], lifecycle}, {paths[7], counter}, {paths[8], legacyAgent}} {
			if err := hub.WriteJSON(worktree, item.path, item.value); err != nil {
				return nil, err
			}
		}
		return paths, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreHubProjectSemantics(ctx, "example", "EXM"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReadLocalAgent(ctx, "example", legacyAgent.AgentID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy Hub Agent was restored into Local state: err=%v", err)
	}
	for _, entity := range []struct {
		entityType string
		id         string
		revision   int64
	}{{"task", task.ID, 1}, {"adr", adr.ID, 2}, {"milestone", milestone.ID, 2}, {"track", track.ID, 3}} {
		stored, err := db.ReadSharedEntity(ctx, entity.entityType, entity.id)
		if err != nil || stored.Revision != entity.revision {
			t.Fatalf("restored %s=%#v err=%v", entity.entityType, stored, err)
		}
	}
	rows, err := db.Shared.Query(ctx, `SELECT created_at,created_by FROM shared_relations WHERE project_id=? AND kind=? AND source_id=? AND target_id=?`, relation.ProjectID, relation.Kind, relation.Source, relation.Target)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][1] != relation.CreatedBy {
		t.Fatalf("restored relation=%#v err=%v", rows.Rows, err)
	}
	events, err := db.ListSharedLifecycleEvents(ctx, "adr", "example", adr.ID, 16)
	if err != nil || len(events) != 1 || events[0].OperationID != lifecycle.OperationID || events[0].ToStatus != model.ADRStatusAccepted {
		t.Fatalf("restored ADR lifecycle evidence=%#v err=%v", events, err)
	}
	for entityType, expected := range map[string]int64{"task": 13, "adr": 9, "milestone": 5, "track": 7, "journal": 22} {
		_, next, found, err := db.ReadSharedSequence(ctx, entityType, "example")
		if err != nil || !found || next != expected {
			t.Fatalf("restored %s sequence next=%d found=%t err=%v, want %d", entityType, next, found, err, expected)
		}
	}
	var restoredTask model.TaskAuthoring
	stored, err := db.ReadSharedEntity(ctx, "task", task.ID)
	if err != nil || json.Unmarshal(stored.Payload, &restoredTask) != nil || restoredTask.Title != task.Title {
		t.Fatalf("restored Task payload=%#v err=%v", restoredTask, err)
	}
	revision, err := db.ReadSharedRevision(ctx, "task", "example", task.ID, 1)
	if err != nil || revision.MutationKind != history.MutationKind || revision.Actor != history.Actor || revision.Reason != history.Reason {
		t.Fatalf("restored Task revision=%#v err=%v", revision, err)
	}
}
