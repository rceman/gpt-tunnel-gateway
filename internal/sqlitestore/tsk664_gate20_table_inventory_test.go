package sqlitestore

import (
	"context"
	"sort"
	"testing"

	"github.com/rceman/go-sqlite-store/store"
)

func TestTSK664Gate20SharedAndLocalTablesAreClassified(t *testing.T) {
	shared := map[string]string{
		"shared_tasks":                  "KEEP",
		"shared_milestones":             "KEEP",
		"shared_tracks":                 "KEEP",
		"shared_trains":                 "KEEP",
		"shared_adrs":                   "KEEP",
		"shared_rules":                  "KEEP",
		"shared_journals":               "KEEP",
		"shared_replication":            "LOCAL_ONLY",
		"hub_outbox":                    "LOCAL_ONLY",
		"replication_state":             "LOCAL_ONLY",
		"shared_authority":              "LOCAL_ONLY",
		"shared_operations":             "LOCAL_ONLY",
		"shared_project_identifiers":    "KEEP",
		"shared_train_task_admissions":  "KEEP",
		"shared_integration_receipts":   "KEEP",
		"shared_bootstrap_markers":      "LOCAL_ONLY",
		"shared_project_configurations": "KEEP",
		"shared_entity_sequences":       "KEEP",
		"shared_entity_revisions":       "KEEP",
		"shared_lifecycle_events":       "KEEP",
		"shared_relations":              "KEEP",
		"shared_upgrade_migrations":     "LOCAL_ONLY",
		"schema_migrations":             "LOCAL_ONLY",
	}
	retiredShared := map[string]string{
		"shared_task_execution_states":        "MIGRATE",
		"shared_task_execution_phases":        "MIGRATE",
		"shared_task_execution_verifications": "MIGRATE",
	}
	local := map[string]string{
		"local_operation_sequences":          "LOCAL_ONLY",
		"local_operations":                   "LOCAL_ONLY",
		"local_events":                       "LOCAL_ONLY",
		"local_messages":                     "LOCAL_ONLY",
		"local_logs":                         "LOCAL_ONLY",
		"local_retention":                    "LOCAL_ONLY",
		"local_callback_epochs":              "LOCAL_ONLY",
		"local_agents":                       "LOCAL_ONLY",
		"local_sessions":                     "LOCAL_ONLY",
		"local_session_bootstrap_grants":     "LOCAL_ONLY",
		"local_token_usage_events":           "LOCAL_ONLY",
		"local_token_usage":                  "LOCAL_ONLY",
		"local_relations":                    "LOCAL_ONLY",
		"plaw_message_sequences":             "LOCAL_ONLY",
		"plaw_messages":                      "LOCAL_ONLY",
		"local_upgrade_migrations":           "LOCAL_ONLY",
		"local_task_execution_states":        "LOCAL_ONLY",
		"local_task_execution_phases":        "LOCAL_ONLY",
		"local_task_execution_verifications": "LOCAL_ONLY",
		"schema_migrations":                  "LOCAL_ONLY",
	}

	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertTableInventory(t, db.Shared, shared)
	assertTableInventory(t, db.Local, local)
	identifierColumns, err := db.sharedProjectIdentifierColumns(context.Background())
	if err != nil || len(identifierColumns) != 2 {
		t.Fatalf("retired Shared project identifier counters remain: columns=%v err=%v", identifierColumns, err)
	}
	for table, disposition := range retiredShared {
		if disposition != "MIGRATE" {
			t.Fatalf("Shared execution family %q has no explicit migration disposition", table)
		}
		exists, err := db.sharedTableExists(context.Background(), table)
		if err != nil || exists {
			t.Fatalf("retired Shared execution family %q remains: exists=%v err=%v", table, exists, err)
		}
	}
	legacySequences := map[string]string{
		"shared_task_sequences": "MIGRATE",
		"shared_adr_sequences":  "MIGRATE",
	}
	for table, disposition := range legacySequences {
		if disposition != "MIGRATE" {
			t.Fatalf("Shared sequence source %q has no explicit migration disposition", table)
		}
		exists, err := db.sharedTableExists(context.Background(), table)
		if err != nil || exists {
			t.Fatalf("migrated Shared sequence source %q remains: exists=%v err=%v", table, exists, err)
		}
	}
}

func assertTableInventory(t *testing.T, database *store.Store, expected map[string]string) {
	t.Helper()
	rows, err := database.Query(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	actual := make(map[string]bool, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 1 {
			t.Fatalf("invalid table inventory row %#v", row)
		}
		name, ok := row[0].(string)
		if !ok {
			t.Fatalf("invalid table name %T", row[0])
		}
		actual[name] = true
	}
	var missing, unclassified []string
	for name, disposition := range expected {
		switch disposition {
		case "KEEP", "MIGRATE", "DELETE", "LOCAL_ONLY":
		default:
			t.Errorf("table %q has invalid Gate-20 disposition %q", name, disposition)
		}
		if !actual[name] {
			missing = append(missing, name)
		}
	}
	for name := range actual {
		if _, ok := expected[name]; !ok {
			unclassified = append(unclassified, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unclassified)
	if len(missing) > 0 || len(unclassified) > 0 {
		t.Fatalf("table inventory mismatch: missing=%v unclassified=%v", missing, unclassified)
	}
}
