package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK480PriorityMigrationClassifiesActiveLegacyValueWithRationale(t *testing.T) {
	db := newTaskSequenceMigrationDB(t)
	now := time.Date(2026, 9, 20, 19, 45, 0, 0, time.UTC)
	task := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "EXM-TSK480", ProjectID: "example", Revision: 1,
		Title: "Legacy priority", Summary: "Legacy priority summary.", Objective: "Reclassify legacy priority.",
		Priority: "high", ADRRelation: model.TaskADRNoRequired, Status: model.TaskAuthoringPlanned,
		CreatedBy: "planner", CreatedAt: now, UpdatedAt: now,
	}
	task.RevisionSHA256, _ = model.HashTaskAuthoring(task)
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, int64(task.Revision), payload, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	migration, err := sharedTaskPriorityMigration(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(context.Background(), db, []migrate.Migration{migration}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(context.Background(), `SELECT revision,payload FROM shared_tasks WHERE id=?`, task.ID)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("migrated task rows=%#v err=%v", rows.Rows, err)
	}
	var migrated model.TaskAuthoring
	if err := json.Unmarshal(rows.Rows[0][1].([]byte), &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.Revision != 2 || migrated.Priority != model.TaskPriorityP1 {
		t.Fatalf("migrated task=%#v", migrated)
	}
	history, err := db.Query(context.Background(), `SELECT revision,payload,reason FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=? ORDER BY revision`, task.ID)
	if err != nil || len(history.Rows) != 2 {
		t.Fatalf("priority audit history=%#v err=%v", history.Rows, err)
	}
	if !bytes.Equal(history.Rows[0][1].([]byte), payload) || !strings.Contains(history.Rows[1][2].(string), "explicitly classifies") {
		t.Fatalf("priority audit history=%#v", history.Rows)
	}
}

func TestTSK480PriorityMigrationRejectsUnclassifiedActiveValueAndPreservesTerminalValue(t *testing.T) {
	db := newTaskSequenceMigrationDB(t)
	now := time.Now().UTC()
	active := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "EXM-TSK481", ProjectID: "example", Revision: 1,
		Title: "Unknown priority", Summary: "Unknown priority summary.", Objective: "Reject unknown priority.",
		Priority: "urgent", ADRRelation: model.TaskADRNoRequired, Status: model.TaskAuthoringPlanned,
		CreatedBy: "planner", CreatedAt: now, UpdatedAt: now,
	}
	active.RevisionSHA256, _ = model.HashTaskAuthoring(active)
	terminal := active
	terminal.ID = "EXM-TSK482"
	terminal.Status = model.TaskAuthoringDone
	terminal.Priority = "legacy-label"
	terminal.RevisionSHA256, _ = model.HashTaskAuthoring(terminal)
	for _, task := range []model.TaskAuthoring{active, terminal} {
		payload, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, int64(task.Revision), payload, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sharedTaskPriorityMigration(context.Background(), db); err == nil || !strings.Contains(err.Error(), "no explicit re-audit classification") {
		t.Fatalf("unknown active priority error=%v", err)
	}
	terminalRows, err := db.Query(context.Background(), `SELECT payload FROM shared_tasks WHERE id=?`, terminal.ID)
	if err != nil || len(terminalRows.Rows) != 1 {
		t.Fatal(err)
	}
	if !bytes.Contains(terminalRows.Rows[0][0].([]byte), []byte(`"legacy-label"`)) {
		t.Fatalf("terminal priority changed: %s", terminalRows.Rows[0][0])
	}
}

func TestTSK480PriorityMigrationIsBounded(t *testing.T) {
	db := newTaskSequenceMigrationDB(t)
	for i := 0; i <= sharedTaskPriorityMigrationMaxRows; i++ {
		id := fmt.Sprintf("EXM-TSK%03d", i+1)
		if _, err := db.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, id, 1, []byte(`{}`), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sharedTaskPriorityMigration(context.Background(), db); err == nil || !strings.Contains(err.Error(), "bounded priority re-audit maximum") {
		t.Fatalf("bounded priority migration error=%v", err)
	}
}
