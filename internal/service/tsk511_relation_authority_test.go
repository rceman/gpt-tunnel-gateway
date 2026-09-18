package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk511Fixture(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	return s, db
}

func tsk511InsertEntity(t *testing.T, db *sqlitestore.Databases, table, id, titleField, title string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"schema_version": 1, "id": id, "project_id": "example", titleField: title})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), fmt.Sprintf(`INSERT INTO %s(id,revision,payload,updated_at) VALUES(?,?,?,?)`, table), id, 1, payload, "2026-09-18T11:23:00Z"); err != nil {
		t.Fatal(err)
	}
}

func tsk511RenameEntity(t *testing.T, db *sqlitestore.Databases, table, id, titleField, title string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"schema_version": 1, "id": id, "project_id": "example", titleField: title})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), fmt.Sprintf(`UPDATE %s SET payload=?,revision=revision+1 WHERE id=?`, table), payload, id); err != nil {
		t.Fatal(err)
	}
}

func tsk511SeedEntities(t *testing.T, db *sqlitestore.Databases) {
	tsk511InsertEntity(t, db, "shared_adrs", "EXM-ADR1", "title", "First ADR")
	tsk511InsertEntity(t, db, "shared_adrs", "EXM-ADR2", "title", "Second ADR")
	tsk511InsertEntity(t, db, "shared_tasks", "EXM-TSK1", "title", "First Task")
	tsk511InsertEntity(t, db, "shared_tasks", "EXM-TSK2", "title", "Second Task")
	tsk511InsertEntity(t, db, "shared_rules", "EXM-RUL1", "name", "First Rule")
	for entityType, next := range map[string]int64{"task": 3, "adr": 3, "rule": 2} {
		if _, err := db.Shared.Exec(context.Background(), `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES(?,?,?,?) ON CONFLICT(entity_type,project_id) DO UPDATE SET next_number=excluded.next_number`, entityType, "example", "EXM", next); err != nil {
			t.Fatal(err)
		}
	}
}

func tsk511RelationCount(t *testing.T, db *sqlitestore.Databases, local bool) int64 {
	t.Helper()
	count, err := db.CountRelations(context.Background(), local)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestTSK511RelationCreateClosedKindsAndIdempotence(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()

	first, err := s.RelationCreate(ctx, RelationCreateInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Kind:      model.RelationKindAuthority,
		Target:    "EXM-ADR1",
		CreatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Source != "EXM-TSK1" || first.Kind != model.RelationKindAuthority || first.Target != "EXM-ADR1" {
		t.Fatalf("first relation create=%#v", first)
	}
	repeat, err := s.RelationCreate(ctx, RelationCreateInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Kind:      model.RelationKindAuthority,
		Target:    "EXM-ADR1",
		CreatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Created {
		t.Fatal("identical relation triple was not idempotent")
	}
	if got := tsk511RelationCount(t, db, false); got != 1 {
		t.Fatalf("shared relation rows=%d, want 1", got)
	}

	for name, in := range map[string]RelationCreateInput{
		"unknown kind":    {ProjectID: "example", Source: "EXM-TSK1", Kind: "depends_on", Target: "EXM-ADR1"},
		"wrong source":    {ProjectID: "example", Source: "EXM-ADR1", Kind: model.RelationKindCorrects, Target: "EXM-TSK1"},
		"wrong target":    {ProjectID: "example", Source: "EXM-TSK1", Kind: model.RelationKindAuthority, Target: "EXM-TSK2"},
		"self relation":   {ProjectID: "example", Source: "EXM-TSK1", Kind: model.RelationKindCorrects, Target: "EXM-TSK1"},
		"unknown target":  {ProjectID: "example", Source: "EXM-TSK1", Kind: model.RelationKindAuthority, Target: "EXM-ADR9"},
		"unknown source":  {ProjectID: "example", Source: "EXM-TSK9", Kind: model.RelationKindAuthority, Target: "EXM-ADR1"},
		"unsupported key": {ProjectID: "example", Source: "EXM-JRN1", Kind: model.RelationKindAuthority, Target: "EXM-ADR1"},
		"foreign project": {ProjectID: "example", Source: "OTHER-TSK1", Kind: model.RelationKindAuthority, Target: "EXM-ADR1"},
	} {
		if _, err := s.RelationCreate(ctx, in); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if got := tsk511RelationCount(t, db, false); got != 1 {
		t.Fatalf("rejected creates changed the shared relation rows: %d", got)
	}
}

func TestTSK511RelationListDirectionsLiveTitlesAndKinds(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()
	for _, in := range []RelationCreateInput{
		{ProjectID: "example", Source: "EXM-TSK1", Kind: model.RelationKindAuthority, Target: "EXM-ADR1"},
		{ProjectID: "example", Source: "EXM-TSK1", Kind: model.RelationKindCorrects, Target: "EXM-TSK2"},
		{ProjectID: "example", Source: "EXM-TSK2", Kind: model.RelationKindCorrects, Target: "EXM-TSK1"},
		{ProjectID: "example", Source: "EXM-ADR2", Kind: model.RelationKindSupersedes, Target: "EXM-ADR1"},
	} {
		if _, err := s.RelationCreate(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	outgoing, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outgoing.Relations) != 2 || outgoing.Relations[model.RelationKindAuthority]["EXM-ADR1"] != "First ADR" || outgoing.Relations[model.RelationKindCorrects]["EXM-TSK2"] != "Second Task" {
		t.Fatalf("outgoing relations=%#v", outgoing.Relations)
	}

	incoming, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Direction: sqlitestore.RelationDirectionIncoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(incoming.Relations) != 1 || incoming.Relations[model.RelationKindCorrects]["EXM-TSK2"] != "Second Task" {
		t.Fatalf("incoming relations=%#v", incoming.Relations)
	}

	both, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Direction: sqlitestore.RelationDirectionBoth,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(both.Relations[model.RelationKindAuthority]) != 1 || len(both.Relations[model.RelationKindCorrects]) != 1 {
		t.Fatalf("both-direction relations=%#v", both.Relations)
	}

	filtered, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Kind:      model.RelationKindCorrects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Relations) != 1 || filtered.Relations[model.RelationKindCorrects] == nil {
		t.Fatalf("kind-filtered relations=%#v", filtered.Relations)
	}
	if _, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Kind:      model.RelationKindSupersedes,
	}); err == nil {
		t.Fatal("kind unavailable for the source family was accepted")
	}
	if _, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Direction: "sideways",
	}); err == nil {
		t.Fatal("invalid direction was accepted")
	}

	tsk511RenameEntity(t, db, "shared_adrs", "EXM-ADR1", "title", "Renamed ADR")
	renamed, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Relations[model.RelationKindAuthority]["EXM-ADR1"] != "Renamed ADR" {
		t.Fatalf("live title was not resolved: %#v", renamed.Relations)
	}

	superseded, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-ADR2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Relations[model.RelationKindSupersedes]["EXM-ADR1"] != "Renamed ADR" {
		t.Fatalf("supersedes relations=%#v", superseded.Relations)
	}

	rule, err := s.RelationCreate(ctx, RelationCreateInput{
		ProjectID: "example",
		Source:    "EXM-TSK2",
		Kind:      model.RelationKindAuthority,
		Target:    "EXM-RUL1",
	})
	if err != nil || !rule.Created {
		t.Fatalf("authority relation to a Rule was rejected: %#v err=%v", rule, err)
	}
	ruleAuthority, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK2",
		Kind:      model.RelationKindAuthority,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ruleAuthority.Relations[model.RelationKindAuthority]["EXM-RUL1"] != "First Rule" {
		t.Fatalf("rule authority relations=%#v", ruleAuthority.Relations)
	}
}

func TestTSK511RelationProjectionFailsClosedBeyondBound(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()
	for i := 0; i <= sqlitestore.RelationListMaxRows; i++ {
		id := fmt.Sprintf("EXM-ADR%d", i+10)
		tsk511InsertEntity(t, db, "shared_adrs", id, "title", "Target "+id)
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_relations(project_id,kind,source_id,target_id,created_at,created_by) VALUES(?,?,?,?,?,?)`, "example", model.RelationKindAuthority, "EXM-TSK1", id, "2026-09-18T11:23:00Z", "planner"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RelationProjection(ctx, "example", "EXM-TSK1"); err == nil {
		t.Fatal("relation projection beyond the bounded limit did not fail closed")
	}
	bounded, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.HasMore || bounded.NextCursor == "" {
		t.Fatalf("bounded relation list did not report continuation: %#v", bounded)
	}
	if len(bounded.Relations[model.RelationKindAuthority]) != DefaultPublicCollectionLimit {
		t.Fatalf("default page size=%d, want %d", len(bounded.Relations[model.RelationKindAuthority]), DefaultPublicCollectionLimit)
	}
}

func TestTSK511HistoricalTaskScalarsAreNotMigrated(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()

	created, _, err := s.taskAuthoringCreateShared(ctx, "tsk511-historical-scalars", TaskAuthoringCreateInput{
		ProjectID:             "example",
		Title:                 "Historical scalars",
		Summary:               "Historical scalar references.",
		Objective:             "Keep Task-owned scalars out of relation authority.",
		ADRRelation:           model.TaskADRImplementsExisting,
		ADRReferences:         []string{"EXM-ADR1"},
		Dependencies:          []string{"EXM-TSK1"},
		PreparationReferences: []string{"EXM-ADR2"},
		CreatedBy:             "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.readSharedTask(ctx, "example", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.ADRReferences) != 1 || stored.ADRReferences[0] != "EXM-ADR1" || len(stored.Dependencies) != 1 || stored.Dependencies[0] != "EXM-TSK1" || len(stored.PreparationReferences) != 1 || stored.PreparationReferences[0] != "EXM-ADR2" {
		t.Fatalf("Task-owned scalar references changed: %#v", stored)
	}
	if got := tsk511RelationCount(t, db, false); got != 0 {
		t.Fatalf("Task-owned scalars were migrated into relation authority: %d rows", got)
	}
	projection, err := s.RelationProjection(ctx, "example", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection) != 0 {
		t.Fatalf("Task-owned scalars projected as relations: %#v", projection)
	}
}

func TestTSK511RelationListPaginatesBoundedAndScopeBound(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		tsk511InsertEntity(t, db, "shared_adrs", fmt.Sprintf("EXM-ADR1%d", i), "title", fmt.Sprintf("Target %d", i))
	}
	for i := 1; i <= 5; i++ {
		if _, err := s.RelationCreate(ctx, RelationCreateInput{
			ProjectID: "example",
			Source:    "EXM-TSK1",
			Kind:      model.RelationKindAuthority,
			Target:    fmt.Sprintf("EXM-ADR1%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 8; page++ {
		result, err := s.RelationList(ctx, RelationListInput{
			ProjectID: "example",
			Source:    "EXM-TSK1",
			Limit:     2,
			Cursor:    cursor,
		})
		if err != nil {
			t.Fatal(err)
		}
		for key := range result.Relations[model.RelationKindAuthority] {
			if seen[key] {
				t.Fatalf("relation %s repeated across pages", key)
			}
			seen[key] = true
		}
		if !result.HasMore {
			if result.NextCursor != "" {
				t.Fatal("terminal page reported a continuation cursor")
			}
			break
		}
		if result.NextCursor == "" {
			t.Fatal("page reported continuation without a cursor")
		}
		cursor = result.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("paginated relations=%d, want 5: %#v", len(seen), seen)
	}
	first, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Limit:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Limit:     2,
		Direction: sqlitestore.RelationDirectionIncoming,
		Cursor:    first.NextCursor,
	}); err == nil {
		t.Fatal("cursor crossed the direction scope")
	}
	if _, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK2",
		Limit:     2,
		Cursor:    first.NextCursor,
	}); err == nil {
		t.Fatal("cursor crossed the source scope")
	}
}

func TestTSK511PMTRelationsAreLocalOnly(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()

	created, err := s.RelationCreate(ctx, RelationCreateInput{
		ProjectID: "example",
		Source:    "EXM-PMT1",
		Kind:      model.RelationKindConcerns,
		Target:    "EXM-TSK1",
		CreatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created {
		t.Fatal("PMT relation was not created")
	}
	if got := tsk511RelationCount(t, db, true); got != 1 {
		t.Fatalf("local relation rows=%d, want 1", got)
	}
	if got := tsk511RelationCount(t, db, false); got != 0 {
		t.Fatalf("PMT relation leaked into the shared authority: %d rows", got)
	}

	listed, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-PMT1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Relations[model.RelationKindConcerns]["EXM-TSK1"] != "First Task" {
		t.Fatalf("PMT outgoing relations=%#v", listed.Relations)
	}

	incoming, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-TSK1",
		Direction: sqlitestore.RelationDirectionIncoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(incoming.Relations) != 0 {
		t.Fatalf("Local-only PMT source leaked into a canonical view: %#v", incoming.Relations)
	}

	pmtIncoming, err := s.RelationList(ctx, RelationListInput{
		ProjectID: "example",
		Source:    "EXM-PMT1",
		Direction: sqlitestore.RelationDirectionIncoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pmtIncoming.Relations) != 0 {
		t.Fatalf("PMT incoming relations=%#v, want none", pmtIncoming.Relations)
	}
}

func TestTSK511RelationCreateSugarIsAtomicAndBothOrNeither(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()

	task, _, err := s.TaskLifecycleCreate(ctx, TaskAuthoringCreateInput{
		ProjectID:      "example",
		Title:          "Sugar task",
		Summary:        "Sugar task summary.",
		Objective:      "Create with an initial relation.",
		ADRRelation:    model.TaskADRNoRequired,
		RelationType:   model.RelationKindAuthority,
		RelationTarget: "EXM-ADR1",
		CreatedBy:      "planner",
	}, "tsk511-sugar-create")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := s.RelationProjection(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projection[model.RelationKindAuthority]["EXM-ADR1"] != "First ADR" {
		t.Fatalf("sugar relation projection=%#v", projection)
	}

	if _, _, err := s.TaskLifecycleCreate(ctx, TaskAuthoringCreateInput{
		ProjectID:    "example",
		Title:        "Half pair task",
		Summary:      "Half pair summary.",
		Objective:    "Reject the half pair.",
		ADRRelation:  model.TaskADRNoRequired,
		RelationType: model.RelationKindAuthority,
		CreatedBy:    "planner",
	}, "tsk511-sugar-half"); err == nil {
		t.Fatal("relation_type without relation_target was accepted")
	}
	if _, _, err := s.TaskLifecycleCreate(ctx, TaskAuthoringCreateInput{
		ProjectID:      "example",
		Title:          "Half pair task",
		Summary:        "Half pair summary.",
		Objective:      "Reject the half pair.",
		ADRRelation:    model.TaskADRNoRequired,
		RelationTarget: "EXM-ADR1",
		CreatedBy:      "planner",
	}, "tsk511-sugar-half-target"); err == nil {
		t.Fatal("relation_target without relation_type was accepted")
	}
	if _, _, err := s.TaskLifecycleCreate(ctx, TaskAuthoringCreateInput{
		ProjectID:      "example",
		Title:          "Bad target task",
		Summary:        "Bad target summary.",
		Objective:      "Reject an unresolved relation target.",
		ADRRelation:    model.TaskADRNoRequired,
		RelationType:   model.RelationKindAuthority,
		RelationTarget: "EXM-ADR9",
		CreatedBy:      "planner",
	}, "tsk511-sugar-bad-target"); err == nil {
		t.Fatal("unresolved relation target was accepted")
	}

	rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_tasks`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := rows.Rows[0][0].(int64); count != 3 {
		t.Fatalf("rejected sugar created tasks: shared_tasks=%d, want 3 (seed plus one sugar task)", count)
	}
	if got := tsk511RelationCount(t, db, false); got != 1 {
		t.Fatalf("rejected sugar created relations: %d", got)
	}

	if _, err := db.Shared.Exec(ctx, `CREATE TRIGGER tsk511_relation_fault BEFORE INSERT ON shared_relations BEGIN SELECT RAISE(ABORT,'injected relation fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskLifecycleCreate(ctx, TaskAuthoringCreateInput{
		ProjectID:      "example",
		Title:          "Fault task",
		Summary:        "Fault task summary.",
		Objective:      "Prove the atomic rollback.",
		ADRRelation:    model.TaskADRNoRequired,
		RelationType:   model.RelationKindAuthority,
		RelationTarget: "EXM-ADR1",
		CreatedBy:      "planner",
	}, "tsk511-sugar-fault"); err == nil {
		t.Fatal("injected relation fault did not fail the create")
	}
	if _, err := db.Shared.Exec(ctx, `DROP TRIGGER tsk511_relation_fault`); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_tasks`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := rows.Rows[0][0].(int64); count != 3 {
		t.Fatalf("relation fault left a partial entity: shared_tasks=%d, want 3", count)
	}
	if got := tsk511RelationCount(t, db, false); got != 1 {
		t.Fatalf("relation fault left a partial relation: %d", got)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE id=?`, "tsk511-sugar-fault")
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := outbox.Rows[0][0].(int64); count != 0 {
		t.Fatal("relation fault left an outbox intent")
	}
}

func TestTSK511ADRCreateSugarUsesOneSharedBatch(t *testing.T) {
	s, db := tsk511Fixture(t)
	tsk511SeedEntities(t, db)
	ctx := context.Background()

	result, err := s.ADRCreate(ctx, ADRCreateInput{
		ADR:            model.ADR{ProjectID: "example", Title: "Replacement ADR", Summary: "Replacement summary.", Context: "Context.", Decision: "Decision.", Consequences: "Consequences.", CreatedBy: "planner", UpdatedBy: "planner"},
		RelationType:   model.RelationKindSupersedes,
		RelationTarget: "EXM-ADR1",
	})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := s.RelationProjection(ctx, "example", result.EntityKey)
	if err != nil {
		t.Fatal(err)
	}
	if projection[model.RelationKindSupersedes]["EXM-ADR1"] != "First ADR" {
		t.Fatalf("ADR sugar relation projection=%#v", projection)
	}
	if _, err := s.ADRCreate(ctx, ADRCreateInput{
		ADR:            model.ADR{ProjectID: "example", Title: "Bad replacement", Summary: "Bad replacement summary.", Context: "Context.", Decision: "Decision.", Consequences: "Consequences.", CreatedBy: "planner", UpdatedBy: "planner"},
		RelationType:   model.RelationKindCorrects,
		RelationTarget: "EXM-ADR1",
	}); err == nil {
		t.Fatal("ADR sugar accepted a Task-only relation kind")
	}
	adrs, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_adrs`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := adrs.Rows[0][0].(int64); count != 3 {
		t.Fatalf("rejected ADR sugar created an ADR: shared_adrs=%d, want 3", count)
	}
}
