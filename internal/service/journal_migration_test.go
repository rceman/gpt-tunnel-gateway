package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk566SeedSharedTask(t *testing.T, s *Service, key string, metadata map[string]string) {
	t.Helper()
	task := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion, ID: key, ProjectID: "example",
		Title: "Standing inbox", Summary: "Standing inbox Task", Objective: "Collect standing journal inbox entries.",
		Type: model.TaskTypeChore, Execution: model.TaskExecutionCanonical, Scope: &model.TaskScope{Files: []string{"docs"}, Modules: []string{"gateway"}},
		Status: model.TaskAuthoringPlanned, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		Metadata: metadata, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	hash, err := model.HashTaskAuthoring(task)
	if err != nil {
		t.Fatal(err)
	}
	task.RevisionSHA256 = hash
	if err := model.ValidateTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.PutSharedProjection(context.Background(), "task", sqlitestore.SharedEntity{ID: key, Revision: 1, Payload: payload, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
}

func plannerNoteData(references []string) json.RawMessage {
	data, err := json.Marshal(map[string]any{
		"summary": "curated owner decision", "decisions": []string{"d"}, "commitments": []string{},
		"facts": []string{}, "assumptions": []string{}, "blockers": []string{},
		"unresolved": []string{}, "next_actions": []string{}, "references": references,
	})
	if err != nil {
		panic(err)
	}
	return data
}

func journalEntryData(t *testing.T, entry model.JournalEntry) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(entry.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestJournalMigrateCuratedReexpressesLegacyEvents(t *testing.T) {
	s, _, _ := testService(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	s.Durability = db

	decision := tsk566SeedOperatorEvent(t, s, operatorEvidenceSeed{
		projectID:  "example",
		kind:       model.OperatorUserTalk,
		summary:    "owner decision",
		content:    model.OperatorJournalContent{Decisions: []string{"approve canonical journal cutover"}},
		references: model.OperatorJournalReferences{},
		actor:      "owner",
	})
	correction := tsk566SeedOperatorEvent(t, s, operatorEvidenceSeed{
		projectID:         "example",
		kind:              model.OperatorCorrection,
		summary:           "correction",
		content:           model.OperatorJournalContent{Facts: []string{"refine the earlier decision"}},
		supersedesEventID: decision.ID,
		references:        model.OperatorJournalReferences{},
		actor:             "owner",
	})

	result, err := s.JournalMigrate(context.Background(), JournalMigrateInput{
		ProjectID: "example",
		Curated: []JournalMigrateCuratedInput{
			{Source: decision.ID, Data: plannerNoteData(nil)},
			{Source: correction.ID, Data: plannerNoteData([]string{"EXM-ADR1"})},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("migrated entries=%#v", result.Entries)
	}
	first := result.Entries[0]
	if first.ID != "EXM-JRN1" || first.Stream != model.JournalStreamPlannerNotes || first.Status != model.JournalStatusPublished {
		t.Fatalf("first entry=%#v", first)
	}
	if first.Actor != JournalMigrationActor || first.Role != JournalMigrationRole || first.SessionID != JournalMigrationSession {
		t.Fatalf("migration provenance impersonated a role: %#v", first)
	}
	refs := journalEntryData(t, first)["references"].([]any)
	if len(refs) != 1 || refs[0] != decision.ID {
		t.Fatalf("references=%#v, want source %s", refs, decision.ID)
	}
	secondRefs := journalEntryData(t, result.Entries[1])["references"].([]any)
	if len(secondRefs) != 2 || secondRefs[0] != correction.ID || secondRefs[1] != "EXM-ADR1" {
		t.Fatalf("curated references=%#v", secondRefs)
	}

	var legacy model.OperatorJournalEvent
	if _, err := s.entityRegistry("example").ReadInto(context.Background(), entity.JournalFamily, decision.ID, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ID != decision.ID || legacy.Kind != model.OperatorUserTalk {
		t.Fatalf("legacy source was rewritten: %#v", legacy)
	}
}

func TestJournalMigrateCuratedFailsClosedOnMissingSource(t *testing.T) {
	s, _, _ := testService(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	s.Durability = db
	if _, err := s.JournalMigrate(context.Background(), JournalMigrateInput{
		ProjectID: "example",
		Curated:   []JournalMigrateCuratedInput{{Source: "EXM-JRN404", Data: plannerNoteData(nil)}},
	}); err == nil || !strings.Contains(err.Error(), "EXM-JRN404") {
		t.Fatalf("missing source did not fail closed: %v", err)
	}
	page, err := s.JournalList(WithAgentSessionID(context.Background(), tsk585PlannerSession(t, s)), JournalListInput{})
	if err == nil && len(page.Items) != 0 {
		t.Fatalf("failed curated migrate wrote entries: %#v", page.Items)
	}
}

func TestJournalMigrateInboxDrainsMetadataAndArchivesTask(t *testing.T) {
	s, _, _ := testService(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	s.Durability = db

	tsk566SeedSharedTask(t, s, "GTW-TSK619", map[string]string{
		"lesson_002": "TASK: EXM-TSK601/EXM-TSK608 | MISTAKE: wall-clock thresholds in tests | WHY: load-sensitive | PREVENTION: use deterministic waits | SCOPE: universal | EVIDENCE: EXM-TSK608 rework.",
		"lesson_001": "TASK: EXM-TSK601 | MISTAKE: no meaningful tests | WHY: happy-path only | PREVENTION: prove actual execution | SCOPE: universal-positive | EVIDENCE: EXM-TSK601 rework.",
		"mode":       "canonical",
	})
	tsk566SeedSharedTask(t, s, "GTW-TSK609", map[string]string{
		"lead_feedback_20260916_10": "Action/workflow: supervision. Problem: ambiguous session identity. Observed cost: rework loops. Evidence: EXM-TSK615 prompt confusion.",
		"lead_feedback_20260914_1":  "Problem: short hashes only. Evidence: EXM-TSK604 required full digests. Impact: token waste. Improve: expose full evidence fields.",
	})

	result, err := s.JournalMigrate(context.Background(), JournalMigrateInput{
		ProjectID: "example",
		InboxTasks: []JournalMigrateInboxTaskInput{
			{Key: "GTW-TSK619", ArchiveReason: "migrated to canonical worker-lessons"},
			{Key: "GTW-TSK609", ArchiveReason: "migrated to canonical lead-friction"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 4 || len(result.ArchivedTasks) != 2 {
		t.Fatalf("migrate result=%#v", result)
	}
	lessons := result.Entries[:2]
	if lessons[0].Stream != model.JournalStreamWorkerLessons || lessons[1].Stream != model.JournalStreamWorkerLessons {
		t.Fatalf("lesson streams=%#v", lessons)
	}
	first := journalEntryData(t, lessons[0])
	if first["task"] != "EXM-TSK601" || first["scope"] != "universal-positive" || first["mistake"] != "no meaningful tests" {
		t.Fatalf("lesson entry=%#v", first)
	}
	evidence := first["evidence"].([]any)
	if len(evidence) != 2 || evidence[1] != "GTW-TSK619:lesson_001" {
		t.Fatalf("lesson evidence=%#v", evidence)
	}
	second := journalEntryData(t, lessons[1])
	secondEvidence := second["evidence"].([]any)
	if len(secondEvidence) != 3 || secondEvidence[1] != "GTW-TSK619:lesson_002" || secondEvidence[2] != "TASK: EXM-TSK601/EXM-TSK608" {
		t.Fatalf("multi-task evidence=%#v", secondEvidence)
	}

	friction := result.Entries[2:]
	if friction[0].Stream != model.JournalStreamLeadFriction || friction[1].Stream != model.JournalStreamLeadFriction {
		t.Fatalf("friction streams=%#v", friction)
	}
	workflowEntry := journalEntryData(t, friction[1])
	if !strings.Contains(workflowEntry["problem"].(string), "ambiguous session identity") || !strings.Contains(workflowEntry["problem"].(string), "supervision") {
		t.Fatalf("friction problem=%#v", workflowEntry)
	}
	if !strings.Contains(workflowEntry["impact"].(string), "Observed cost") {
		t.Fatalf("friction impact=%#v", workflowEntry)
	}
	basicEntry := journalEntryData(t, friction[0])
	if basicEntry["impact"] != "token waste." || basicEntry["proposed_improvement"] != "expose full evidence fields." {
		t.Fatalf("friction entry=%#v", basicEntry)
	}

	for _, key := range []string{"GTW-TSK619", "GTW-TSK609"} {
		shared, err := s.Durability.ReadSharedTask(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		var task model.TaskAuthoring
		if err := json.Unmarshal(shared.Payload, &task); err != nil {
			t.Fatal(err)
		}
		if task.Status != model.TaskAuthoringArchived {
			t.Fatalf("inbox Task %s status=%q, want archived", key, task.Status)
		}
	}
}

func TestJournalMigrateInboxFailsClosedOnMalformedMetadata(t *testing.T) {
	s, _, _ := testService(t)
	db := testServiceWithDurability(t, s)
	defer db.Close()
	s.Durability = db
	tsk566SeedSharedTask(t, s, "GTW-TSK619", map[string]string{"lesson_001": "MISTAKE: missing fields only"})
	if _, err := s.JournalMigrate(context.Background(), JournalMigrateInput{
		ProjectID:  "example",
		InboxTasks: []JournalMigrateInboxTaskInput{{Key: "GTW-TSK619", ArchiveReason: "drain to worker-lessons"}},
	}); err == nil || !strings.Contains(err.Error(), "lesson_001") {
		t.Fatalf("malformed lesson did not fail closed: %v", err)
	}
	shared, err := s.Durability.ReadSharedTask(context.Background(), "GTW-TSK619")
	if err != nil {
		t.Fatal(err)
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(shared.Payload, &task); err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskAuthoringPlanned {
		t.Fatalf("failed migrate archived the Task: %#v", task.Status)
	}
}
