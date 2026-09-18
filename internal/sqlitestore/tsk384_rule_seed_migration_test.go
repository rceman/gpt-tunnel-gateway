package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk384SeedFixtureConfig() model.ProjectConfiguration {
	now := time.Date(2026, 9, 18, 13, 39, 0, 0, time.UTC)
	configuration := model.DefaultProjectConfiguration("example", now)
	configuration.ProjectID = "example"
	configuration.Revision = 3
	configuration.Workflow.CI.Release = model.WorkflowCIModeRequire
	configuration.Workflow.CI.Task = model.WorkflowCIModeObserve
	configuration.Workflow.CI.TaskMerge = model.WorkflowCIModeDisabled
	configuration.Workflow.WaitForCI = true
	configuration.Workflow.IntegrationBranch = "integration/1"
	configuration.Workflow.WorkflowStage = model.WorkflowStageTransitionalMain
	return configuration
}

func tsk384WriteSeedProvenance(t *testing.T, db *Databases) {
	t.Helper()
	ctx := context.Background()
	configuration := tsk384SeedFixtureConfig()
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", SharedEntity{
		ID:        "example",
		Revision:  int64(configuration.Revision),
		Payload:   payload,
		UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_project_identifiers(project_id,project_code,next_task_number,next_adr_number,next_rule_number,next_journal_number,next_train_number) VALUES(?,?,?,?,?,?,?)`, "example", "EXM", 1, 1, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
}

func tsk384SeededRules(t *testing.T, db *Databases) map[string]model.Rule {
	t.Helper()
	rows, err := db.Shared.Query(context.Background(), `SELECT payload FROM shared_rules ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	rules := make(map[string]model.Rule, len(rows.Rows))
	for _, row := range rows.Rows {
		raw, ok := row[0].([]byte)
		if !ok {
			t.Fatalf("unexpected rule payload column: %#v", row)
		}
		var rule model.Rule
		if err := json.Unmarshal(raw, &rule); err != nil {
			t.Fatal(err)
		}
		rules[rule.Name] = rule
	}
	return rules
}

func TestTSK384RuleSeedMigrationDecomposesExactPolicyLeaves(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.Shared.Exec(ctx, `DELETE FROM schema_migrations WHERE version=?`, sharedRuleSeedMigrationVersion); err != nil {
		t.Fatal(err)
	}
	tsk384WriteSeedProvenance(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rules := tsk384SeededRules(t, db)
	want := map[string]string{
		"agent.wait_for_ci":  `true`,
		"ci.release":         `"require"`,
		"ci.task":            `"observe"`,
		"ci.task_merge":      `"disabled"`,
		"integration_branch": `"integration/1"`,
		"workflow_stage":     `"transitional_main"`,
	}
	configuration := tsk384SeedFixtureConfig()
	if len(rules) != len(want) {
		t.Fatalf("seeded rules=%#v", rules)
	}
	for name, wantValue := range want {
		rule, ok := rules[name]
		if !ok {
			t.Fatalf("missing seeded rule %q: %#v", name, rules)
		}
		if rule.Status != model.RuleStatusAccepted || rule.Title != name || rule.ProjectID != "example" || rule.Revision != 1 {
			t.Fatalf("seeded rule %q has wrong shape: %#v", name, rule)
		}
		if string(rule.Value) != wantValue {
			t.Fatalf("seeded rule %q value=%s want=%s", name, rule.Value, wantValue)
		}
		var decoded any
		if err := json.Unmarshal(rule.Value, &decoded); err != nil {
			t.Fatal(err)
		}
		if name == "agent.wait_for_ci" {
			if value, ok := decoded.(bool); !ok || !value {
				t.Fatalf("boolean policy leaf was not preserved as a typed value: %#v", decoded)
			}
		}
		if !rule.CreatedAt.Equal(configuration.UpdatedAt) {
			t.Fatalf("seeded rule %q does not carry provenance timestamps: %#v", name, rule)
		}
	}
	// deterministic GTW-RUL<n> key allocation in sorted leaf order
	for index, name := range []string{"agent.wait_for_ci", "ci.release", "ci.task", "ci.task_merge", "integration_branch", "workflow_stage"} {
		wantID := fmt.Sprintf("EXM-RUL%d", index+1)
		if rules[name].ID != wantID {
			t.Fatalf("seeded rule %q id=%s want=%s", name, rules[name].ID, wantID)
		}
	}
	// lifecycle history + outbox + sequence for the seeded entities
	history, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_type='rule' AND mutation_kind='create' AND project_id='example'`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := history.Rows[0][0].(int64); count != int64(len(want)) {
		t.Fatalf("seeded rule history rows=%v", history.Rows)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE entity_type='rule' AND kind='rule-create'`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := outbox.Rows[0][0].(int64); count != int64(len(want)) {
		t.Fatalf("seeded rule outbox rows=%v", outbox.Rows)
	}
	sequence, err := db.Shared.Query(ctx, `SELECT next_number FROM shared_entity_sequences WHERE entity_type='rule' AND project_id='example'`)
	if err != nil {
		t.Fatal(err)
	}
	if next, _ := sequence.Rows[0][0].(int64); next != 7 {
		t.Fatalf("seeded rule sequence=%v", sequence.Rows)
	}

	// reopen does not duplicate seeds
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if rules := tsk384SeededRules(t, db); len(rules) != len(want) {
		t.Fatalf("reopen duplicated seeded rules: %#v", rules)
	}
}

func TestTSK384RuleSeedMigrationIsBoundedWithoutConfigOrIdentifiers(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Shared.Query(context.Background(), `SELECT COUNT(*) FROM shared_rules`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := rows.Rows[0][0].(int64); count != 0 {
		t.Fatalf("fresh store seeded rules without provenance: %d", count)
	}
}

func TestTSK384RuleNameUniquenessIsEnforcedAtomically(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	payload := func(id string) []byte {
		raw, err := json.Marshal(map[string]any{"schema_version": 1, "id": id, "project_id": "example", "revision": 1, "title": "t", "summary": "s", "status": "accepted", "name": "ci.release", "value": "release"})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "EXM-RUL1", 1, payload("EXM-RUL1"), "2026-09-18T13:39:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "EXM-RUL2", 1, payload("EXM-RUL2"), "2026-09-18T13:39:00Z"); err == nil {
		t.Fatal("duplicate rule name accepted by the unique index")
	}
	// narrative rules share no name identity and never collide on NULL
	narrative, err := json.Marshal(map[string]any{"schema_version": 1, "id": "EXM-RUL2", "project_id": "example", "revision": 1, "title": "t", "summary": "s", "status": "proposed", "description": "body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "EXM-RUL2", 1, narrative, "2026-09-18T13:39:00Z"); err != nil {
		t.Fatalf("narrative rule rejected by name index: %v", err)
	}
}
