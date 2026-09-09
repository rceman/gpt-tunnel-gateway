package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK531TaskCreateUsesReconciledSequence(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	legacy := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "EXM-TSK550", ProjectID: "example", Revision: 1, Title: "Retained highest task", Summary: "A retained highest task.", Objective: "A retained highest task objective.", ADRRelation: model.TaskADRNoRequired, Status: model.TaskAuthoringPlanned, CreatedBy: "planner", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	legacy.RevisionSHA256, err = model.HashTaskAuthoring(legacy)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	payload, err := json.Marshal(legacy)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, legacy.ID, 1, payload, legacy.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_entity_sequences SET next_number=? WHERE entity_type='task' AND project_id=?`, 546, "example"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM schema_migrations WHERE name=?`, "reconcile retained task sequences"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	created, _, err := s.taskAuthoringCreateShared(ctx, "tsk531-sequence-create", TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "New allocated task",
		Summary:     "A newly allocated task.",
		Objective:   "Allocate after retained tasks.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "EXM-TSK551" {
		t.Fatalf("created ID=%q, want EXM-TSK551", created.ID)
	}
}
