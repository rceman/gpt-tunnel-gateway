package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK649ReconcileProjectBootstrapRepairsMissingLeavesWithoutReplacingExistingRule(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 20, 21, 0, 0, 0, time.UTC)
	configuration := model.DefaultProjectConfiguration("example", now)
	definition, ok := sharedLifecycle("rule")
	if !ok {
		t.Fatal("rule lifecycle descriptor unavailable")
	}
	existing, err := sharedRuleSeedStatementsForLeaves(definition, configuration, "EXM", sharedRuleSeedLeaves(configuration)[:1], len(sharedRuleSeedLeaves(configuration)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Batch(ctx, existing); err != nil {
		t.Fatal(err)
	}
	beforeRows, err := db.Shared.Query(ctx, `SELECT payload FROM shared_rules WHERE id=?`, "EXM-RUL1")
	if err != nil || len(beforeRows.Rows) != 1 {
		t.Fatalf("existing rule=%#v err=%v", beforeRows.Rows, err)
	}
	before := append([]byte(nil), beforeRows.Rows[0][0].([]byte)...)
	identifiers := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: "example", ProjectCode: "EXM", NextTaskNumber: 1, NextADRNumber: 1}
	if err := db.ReconcileProjectBootstrap(ctx, ProjectBootstrapUpdate{
		ProjectID:           "example",
		PreviousProjectCode: "EXM",
		ProjectCode:         "EXM",
		HubIdentifiers:      identifiers,
		Configuration:       configuration,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Shared.Query(ctx, `SELECT id,payload FROM shared_rules WHERE json_extract(payload,'$.project_id')=? ORDER BY id`, "example")
	if err != nil || len(rows.Rows) != 6 {
		t.Fatalf("repaired rules=%#v err=%v", rows.Rows, err)
	}
	if !bytes.Equal(before, rows.Rows[0][1].([]byte)) {
		t.Fatalf("existing rule was replaced: before=%s after=%s", before, rows.Rows[0][1])
	}
	var repaired model.Rule
	if err := json.Unmarshal(rows.Rows[5][1].([]byte), &repaired); err != nil || repaired.Name != "workflow_stage" {
		t.Fatalf("last repaired rule=%#v err=%v", repaired, err)
	}
}
