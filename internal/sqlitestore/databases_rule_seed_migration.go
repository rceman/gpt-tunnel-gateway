package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const sharedRuleSeedMaxProjects = 64

// sharedRuleSeedLeaf is one decomposed workflow-policy leaf materialized as
// an accepted named Rule. The typed value preserves the exact policy leaf;
// document metadata (project_id, revision, schema_version, updated_*) stays
// provenance only.
type sharedRuleSeedLeaf struct {
	number  int
	name    string
	summary string
	value   any
}

func sharedRuleSeedLeaves(configuration model.ProjectConfiguration) []sharedRuleSeedLeaf {
	return []sharedRuleSeedLeaf{
		{number: 1, name: "agent.wait_for_ci", summary: "Agent may wait for CI", value: false},
		{number: 2, name: "ci.release", summary: "Release CI mode", value: model.WorkflowCIModeDisabled},
		{number: 3, name: "ci.task", summary: "Task CI mode", value: model.WorkflowCIModeDisabled},
		{number: 4, name: "ci.task_merge", summary: "Integration CI mode", value: model.WorkflowCIModeDisabled},
		{number: 5, name: "integration_branch", summary: "Integration branch", value: "main"},
		{number: 6, name: "workflow_stage", summary: "Workflow stage", value: model.WorkflowStageTransitionalMain},
	}
}

// sharedRuleSeedMigration decomposes each retained project workflow-policy
// leaf into one accepted named canonical Rule. After this migration the
// aggregate policy document is never consulted as rule authority; it remains
// provenance only.
func sharedRuleSeedMigration(ctx context.Context, db *upstream.Store) (migrate.Migration, error) {
	migration := migrate.Migration{Version: sharedRuleSeedMigrationVersion, Name: sharedRuleSeedMigrationName}
	definition, ok := sharedLifecycle("rule")
	if !ok || definition.StateTable == "" || definition.HistoryTable == "" || definition.SequenceTable == "" {
		return migration, fmt.Errorf("shared rule lifecycle descriptor is unavailable")
	}
	migration.Statements = append(migration.Statements, upstream.Statement{SQL: `CREATE UNIQUE INDEX IF NOT EXISTS shared_rules_name_idx ON shared_rules(json_extract(payload,'$.project_id'), json_extract(payload,'$.name')) WHERE json_extract(payload,'$.name') IS NOT NULL`})
	projects, err := db.Query(ctx, `SELECT id FROM shared_project_configurations ORDER BY id LIMIT ?`, int64(sharedRuleSeedMaxProjects+1))
	if err != nil {
		return migration, fmt.Errorf("read retained project configurations: %w", err)
	}
	if len(projects.Rows) > sharedRuleSeedMaxProjects {
		return migration, fmt.Errorf("project configuration inventory exceeds bounded migration maximum %d", sharedRuleSeedMaxProjects)
	}
	for _, row := range projects.Rows {
		if len(row) != 1 {
			return migration, fmt.Errorf("invalid shared project configuration row")
		}
		projectID, ok := row[0].(string)
		if !ok || model.ValidateProjectIdentifier(projectID) != nil {
			return migration, fmt.Errorf("invalid shared project configuration id")
		}
		rules, err := db.Query(ctx, `SELECT payload FROM shared_rules WHERE json_extract(payload,'$.project_id')=? AND json_extract(payload,'$.name') IN ('agent.wait_for_ci','ci.release','ci.task','ci.task_merge','integration_branch','workflow_stage')`, projectID)
		if err != nil {
			return migration, fmt.Errorf("read canonical workflow Rules for %s: %w", projectID, err)
		}
		found := make(map[string]bool, len(rules.Rows))
		for _, ruleRow := range rules.Rows {
			if len(ruleRow) != 1 {
				return migration, fmt.Errorf("invalid canonical workflow Rule row for %s", projectID)
			}
			payload, ok := ruleRow[0].([]byte)
			if !ok {
				if text, isText := ruleRow[0].(string); isText {
					payload, ok = []byte(text), true
				}
			}
			if !ok {
				return migration, fmt.Errorf("invalid canonical workflow Rule payload for %s", projectID)
			}
			var rule model.Rule
			if err := json.Unmarshal(payload, &rule); err != nil || model.ValidateRule(rule) != nil || rule.ProjectID != projectID {
				return migration, fmt.Errorf("canonical workflow Rule for %s is invalid", projectID)
			}
			found[rule.Name] = true
		}
		for _, name := range []string{"agent.wait_for_ci", "ci.release", "ci.task", "ci.task_merge", "integration_branch", "workflow_stage"} {
			if !found[name] {
				return migration, fmt.Errorf("canonical workflow Rule %q is missing for %s; refusing to invent policy defaults", name, projectID)
			}
		}
	}
	return migration, nil
}

func readSharedRuleSeedIdentifiers(ctx context.Context, db *upstream.Store) (map[string]string, error) {
	rows, err := db.Query(ctx, `SELECT project_id,project_code FROM shared_project_identifiers ORDER BY project_id LIMIT ?`, int64(sharedRuleSeedMaxProjects+1))
	if err != nil {
		return nil, fmt.Errorf("read shared project identifiers: %w", err)
	}
	if len(rows.Rows) > sharedRuleSeedMaxProjects {
		return nil, fmt.Errorf("project identifier inventory exceeds bounded migration maximum %d", sharedRuleSeedMaxProjects)
	}
	identifiers := make(map[string]string, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 2 {
			return nil, fmt.Errorf("invalid shared project identifiers row")
		}
		projectID, idOK := row[0].(string)
		projectCode, codeOK := row[1].(string)
		if !idOK || !codeOK || model.ValidateProjectIdentifier(projectID) != nil || model.ValidateProjectCode(projectCode) != nil {
			return nil, fmt.Errorf("invalid shared project identifiers row")
		}
		identifiers[projectID] = projectCode
	}
	return identifiers, nil
}

func readSharedRuleSeedExisting(ctx context.Context, db *upstream.Store) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT json_extract(payload,'$.project_id') FROM shared_rules`)
	if err != nil {
		return nil, fmt.Errorf("read retained rules: %w", err)
	}
	existing := make(map[string]bool, len(rows.Rows))
	for _, row := range rows.Rows {
		if projectID, ok := row[0].(string); ok {
			existing[projectID] = true
		}
	}
	return existing, nil
}

func sharedRuleSeedStatements(definition sharedLifecycleDefinition, configuration model.ProjectConfiguration, projectCode string) ([]upstream.Statement, error) {
	leaves := sharedRuleSeedLeaves(configuration)
	return sharedRuleSeedStatementsForLeaves(definition, configuration, projectCode, leaves, len(leaves))
}

func sharedRuleSeedStatementsForLeaves(definition sharedLifecycleDefinition, configuration model.ProjectConfiguration, projectCode string, leaves []sharedRuleSeedLeaf, reservedLeaves int) ([]upstream.Statement, error) {
	projectID := configuration.ProjectID
	recorded := configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if configuration.UpdatedAt.IsZero() {
		recorded = time.Now().UTC().Format(time.RFC3339Nano)
	}
	changedFields, err := json.Marshal([]string{"name", "title", "summary", "value"})
	if err != nil {
		return nil, err
	}
	statements := make([]upstream.Statement, 0, 19)
	for index, leaf := range leaves {
		if err := model.ValidateRuleName(leaf.name); err != nil {
			return nil, err
		}
		rawValue, err := json.Marshal(leaf.value)
		if err != nil {
			return nil, err
		}
		if err := model.ValidateRuleValue(rawValue); err != nil {
			return nil, err
		}
		number := leaf.number
		if number == 0 {
			number = index + 1
		}
		entityID := fmt.Sprintf("%s-RUL%d", projectCode, number)
		rule := model.Rule{
			SchemaVersion: model.SchemaVersion,
			ID:            entityID,
			ProjectID:     projectID,
			Revision:      1,
			Title:         leaf.name,
			Summary:       leaf.summary,
			Status:        model.RuleStatusAccepted,
			Name:          leaf.name,
			Value:         rawValue,
			CreatedBy:     "migration",
			CreatedAt:     configuration.UpdatedAt,
			UpdatedAt:     configuration.UpdatedAt,
			LastReason:    "seed",
		}
		if rule.CreatedAt.IsZero() {
			rule.CreatedAt = time.Now().UTC()
			rule.UpdatedAt = rule.CreatedAt
		}
		if err := model.ValidateRule(rule); err != nil {
			return nil, fmt.Errorf("seeded rule %s is invalid: %w", entityID, err)
		}
		payload, err := json.Marshal(rule)
		if err != nil {
			return nil, err
		}
		operationID := fmt.Sprintf("rule-seed-%s", entityID)
		statements = append(statements,
			upstream.Statement{SQL: `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, Args: []any{entityID, 1, payload, recorded}, RequireRowsAffected: 1},
			sharedHistoryInsertStatement(definition, entityID, projectID, 1, "create", "migration", "seed", changedFields, payload, recorded),
			upstream.Statement{SQL: `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{operationID, "rule", entityID, 1, "rule-create", payload, recorded}, RequireRowsAffected: 1},
		)
	}
	// The sequence reserve is safe to re-apply inside a caller batch: when a
	// rule sequence already exists (for example a project that authored
	// narrative rules before its machine leaves were seeded) the conflict
	// clause keeps the correct code and lifts next_number past the seeded ids.
	statements = append(statements, upstream.Statement{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES(?,?,?,?) ON CONFLICT(entity_type,project_id) DO UPDATE SET project_code=excluded.project_code, next_number=MAX(shared_entity_sequences.next_number, excluded.next_number)`, Args: []any{"rule", projectID, projectCode, int64(reservedLeaves + 1)}})
	return statements, nil
}

func sharedRuleSeedMissingStatements(ctx context.Context, d *Databases, definition sharedLifecycleDefinition, configuration model.ProjectConfiguration, projectCode string) ([]upstream.Statement, error) {
	rows, err := d.Shared.Query(ctx, `SELECT payload FROM shared_rules WHERE json_extract(payload,'$.project_id')=? AND json_extract(payload,'$.name') IS NOT NULL`, configuration.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("read existing Shared machine-policy Rules: %w", err)
	}
	leaves := sharedRuleSeedLeaves(configuration)
	leafNames := make(map[string]bool, len(leaves))
	for _, leaf := range leaves {
		leafNames[leaf.name] = true
	}
	existing := make(map[string]bool, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 1 {
			return nil, fmt.Errorf("invalid Shared machine-policy Rule row")
		}
		payload, ok := row[0].([]byte)
		if !ok {
			return nil, fmt.Errorf("invalid Shared machine-policy Rule payload")
		}
		var rule model.Rule
		if err := json.Unmarshal(payload, &rule); err != nil {
			return nil, fmt.Errorf("decode existing Shared machine-policy Rule: %w", err)
		}
		if err := model.ValidateRule(rule); err != nil {
			return nil, fmt.Errorf("existing Shared machine-policy Rule %q is invalid: %w", rule.ID, err)
		}
		if leafNames[rule.Name] {
			existing[rule.Name] = true
		}
	}
	missing := make([]sharedRuleSeedLeaf, 0, len(leaves))
	for _, leaf := range leaves {
		if !existing[leaf.name] {
			missing = append(missing, leaf)
		}
	}
	return sharedRuleSeedStatementsForLeaves(definition, configuration, projectCode, missing, len(leaves))
}

// SeedSharedRulesFromConfiguration applies the canonical workflow-policy leaf
// decomposition in one Shared batch. Production callers keep the statements
// inside their own atomic batches (bootstrap establishment, migration); this
// helper exists for test fixtures that establish a configured project
// directly.
func (d *Databases) SeedSharedRulesFromConfiguration(ctx context.Context, configuration model.ProjectConfiguration, projectCode string) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	definition, ok := sharedLifecycle("rule")
	if !ok {
		return fmt.Errorf("shared rule lifecycle descriptor is unavailable")
	}
	statements, err := sharedRuleSeedStatements(definition, configuration, projectCode)
	if err != nil {
		return err
	}
	_, err = d.Shared.Batch(ctx, statements)
	return err
}
