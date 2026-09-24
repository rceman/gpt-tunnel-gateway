package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// RelationListMaxRows bounds one relation list read. Exceeding the bound fails
// closed rather than truncating silently.
const RelationListMaxRows = 256

const (
	RelationDirectionOutgoing = "outgoing"
	RelationDirectionIncoming = "incoming"
	RelationDirectionBoth     = "both"
)

type RelationRow struct {
	Kind      string
	Direction string
	Other     string
	CreatedAt string
	CreatedBy string
}

// RelationInsertStatement builds the idempotent relation insert used both by
// relation/create and by the atomic entity-create shortcut.
func RelationInsertStatement(local bool, projectID, kind, source, target, createdAt, createdBy string) upstream.Statement {
	table := relationTable(local)
	return upstream.Statement{
		SQL:  fmt.Sprintf(`INSERT INTO %s(project_id,kind,source_id,target_id,created_at,created_by) VALUES(?,?,?,?,?,?) ON CONFLICT(project_id,kind,source_id,target_id) DO NOTHING`, table),
		Args: []any{projectID, kind, source, target, createdAt, createdBy},
	}
}

func relationTable(local bool) string {
	if local {
		return "local_relations"
	}
	return "shared_relations"
}

func relationStore(d *Databases, local bool) (*upstream.Store, error) {
	if d == nil {
		return nil, fmt.Errorf("relation store is unavailable")
	}
	if local {
		if d.Local == nil {
			return nil, fmt.Errorf("local store is unavailable")
		}
		return d.Local, nil
	}
	if d.Shared == nil {
		return nil, fmt.Errorf("shared store is unavailable")
	}
	return d.Shared, nil
}

// CreateRelation records one directed relation row. Identical triples are
// idempotent: the insert reports created=false when the row already exists, so
// concurrent identical creates cannot both claim creation.
func (d *Databases) CreateRelation(ctx context.Context, local bool, projectID, kind, source, target, createdBy string, now time.Time) (bool, error) {
	store, err := relationStore(d, local)
	if err != nil {
		return false, err
	}
	statement := RelationInsertStatement(local, projectID, kind, source, target, now.UTC().Format(time.RFC3339Nano), createdBy)
	results, err := store.Batch(ctx, []upstream.Statement{statement})
	if err != nil {
		return false, err
	}
	if len(results) != 1 {
		return false, fmt.Errorf("invalid relation insert result")
	}
	return results[0].RowsAffected == 1, nil
}

func (d *Databases) EnsureSharedRelation(ctx context.Context, relation model.Relation) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared relation store is unavailable")
	}
	if err := model.ValidateRelation(relation); err != nil {
		return err
	}
	if family, err := model.RelationFamilyOf(relation.Source); err != nil || family == model.RelationFamilyPMT {
		return fmt.Errorf("Local-only relation cannot be restored to Shared")
	}
	rows, err := d.Shared.Query(ctx, `SELECT created_at,created_by FROM shared_relations WHERE project_id=? AND kind=? AND source_id=? AND target_id=?`, relation.ProjectID, relation.Kind, relation.Source, relation.Target)
	if err != nil {
		return err
	}
	if len(rows.Rows) > 0 {
		if len(rows.Rows) != 1 || len(rows.Rows[0]) != 2 {
			return fmt.Errorf("invalid Shared relation row")
		}
		createdAt, timeOK := rows.Rows[0][0].(string)
		createdBy, actorOK := rows.Rows[0][1].(string)
		if !timeOK || !actorOK {
			return fmt.Errorf("invalid Shared relation values")
		}
		storedTime, parseErr := time.Parse(time.RFC3339Nano, createdAt)
		if parseErr != nil || !storedTime.Equal(relation.CreatedAt) || createdBy != relation.CreatedBy {
			return fmt.Errorf("Shared relation conflicts with Hub authority")
		}
		return nil
	}
	_, err = d.CreateRelation(ctx, false, relation.ProjectID, relation.Kind, relation.Source, relation.Target, relation.CreatedBy, relation.CreatedAt)
	return err
}

const (
	sharedRelationHubOutboxMigrationID = "shared_relations_hub_outbox_v1"
	sharedRelationHubOutboxMaxRows     = 4096
	sharedRelationHubOutboxBatchSize   = 128
)

func relationOutboxStatement(relation model.Relation) (upstream.Statement, error) {
	if err := model.ValidateRelation(relation); err != nil {
		return upstream.Statement{}, err
	}
	if family, err := model.RelationFamilyOf(relation.Source); err != nil || family == model.RelationFamilyPMT {
		return upstream.Statement{}, fmt.Errorf("Local-only relation cannot be published to Hub")
	}
	payload, err := json.Marshal(relation)
	if err != nil {
		return upstream.Statement{}, err
	}
	identity := relation.Identity()
	id := "relation-" + identity
	return upstream.Statement{
		SQL:  `INSERT OR IGNORE INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at,operation_id,request_sha256) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		Args: []any{id, "relation", identity, relation.ProjectID, 1, "relation-create", payload, relation.CreatedAt.UTC().Format(time.RFC3339Nano), id, ""},
	}, nil
}

func (d *Databases) MigrateSharedRelationsToHubOutbox(ctx context.Context) error {
	if d == nil || d.Shared == nil {
		return fmt.Errorf("Shared store is required for relation publication migration")
	}
	state, err := d.sharedUpgradeMigrationState(ctx, sharedRelationHubOutboxMigrationID)
	if err != nil {
		return err
	}
	if state == "complete" {
		return nil
	}
	if err := d.setSharedUpgradeMigrationState(ctx, sharedRelationHubOutboxMigrationID, "in_progress"); err != nil {
		return err
	}
	rows, err := d.Shared.Query(ctx, `SELECT project_id,kind,source_id,target_id,created_at,created_by FROM shared_relations ORDER BY project_id,kind,source_id,target_id LIMIT ?`, int64(sharedRelationHubOutboxMaxRows+1))
	if err != nil {
		return err
	}
	if len(rows.Rows) > sharedRelationHubOutboxMaxRows {
		return fmt.Errorf("Shared relation publication migration exceeds bounded row maximum")
	}
	statements := make([]upstream.Statement, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 6 {
			return fmt.Errorf("invalid Shared relation row")
		}
		projectID, projectOK := row[0].(string)
		kind, kindOK := row[1].(string)
		source, sourceOK := row[2].(string)
		target, targetOK := row[3].(string)
		createdAt, createdAtOK := row[4].(string)
		createdBy, createdByOK := row[5].(string)
		if !projectOK || !kindOK || !sourceOK || !targetOK || !createdAtOK || !createdByOK {
			return fmt.Errorf("invalid Shared relation row values")
		}
		timestamp, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return fmt.Errorf("invalid Shared relation timestamp: %w", err)
		}
		relation := model.Relation{SchemaVersion: model.RelationSchemaVersion, ProjectID: projectID, Kind: kind, Source: source, Target: target, CreatedAt: timestamp.UTC(), CreatedBy: createdBy}
		statement, err := relationOutboxStatement(relation)
		if err != nil {
			return fmt.Errorf("invalid Shared relation: %w", err)
		}
		statements = append(statements, statement)
	}
	for start := 0; start < len(statements); start += sharedRelationHubOutboxBatchSize {
		end := start + sharedRelationHubOutboxBatchSize
		if end > len(statements) {
			end = len(statements)
		}
		if _, err := d.Shared.Batch(ctx, statements[start:end]); err != nil {
			return err
		}
	}
	return d.setSharedUpgradeMigrationState(ctx, sharedRelationHubOutboxMigrationID, "complete")
}

func (d *Databases) relationDirectionPage(ctx context.Context, local bool, projectID, self string, kinds []string, outgoing, includeEqual bool, afterKind, afterOther string, limit int) ([]RelationRow, error) {
	if limit < 1 || limit > RelationListMaxRows+1 {
		return nil, fmt.Errorf("invalid relation list limit")
	}
	store, err := relationStore(d, local)
	if err != nil {
		return nil, err
	}
	selfColumn, otherColumn := "source_id", "target_id"
	if !outgoing {
		selfColumn, otherColumn = "target_id", "source_id"
	}
	where := []string{"project_id = ?", selfColumn + " = ?"}
	args := []any{projectID, self}
	if len(kinds) > 0 {
		placeholders := make([]string, len(kinds))
		for i, kind := range kinds {
			placeholders[i] = "?"
			args = append(args, kind)
		}
		where = append(where, "kind IN ("+strings.Join(placeholders, ",")+")")
	}
	if afterKind != "" {
		operator := ">"
		if includeEqual {
			operator = ">="
		}
		where = append(where, fmt.Sprintf("(kind > ? OR (kind = ? AND %s %s ?))", otherColumn, operator))
		args = append(args, afterKind, afterKind, afterOther)
	}
	args = append(args, limit)
	rows, err := store.Query(ctx, fmt.Sprintf(`SELECT kind,%s,created_at,created_by FROM %s WHERE %s ORDER BY kind,%s LIMIT ?`, otherColumn, relationTable(local), strings.Join(where, " AND "), otherColumn), args...)
	if err != nil {
		return nil, err
	}
	direction := RelationDirectionIncoming
	if outgoing {
		direction = RelationDirectionOutgoing
	}
	result := make([]RelationRow, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid relation row")
		}
		kind, kindOK := row[0].(string)
		other, otherOK := row[1].(string)
		createdAt, createdOK := row[2].(string)
		createdBy, createdByOK := row[3].(string)
		if !kindOK || !otherOK || !createdOK || !createdByOK {
			return nil, fmt.Errorf("invalid relation row")
		}
		result = append(result, RelationRow{
			Kind:      kind,
			Direction: direction,
			Other:     other,
			CreatedAt: createdAt,
			CreatedBy: createdBy,
		})
	}
	return result, nil
}

// ListRelationPage reads one bounded keyset page of relation rows under the
// deterministic (kind, other, direction) ordering. When both directions are
// requested the two bounded pages are merged without materializing either
// direction in full.
func (d *Databases) ListRelationPage(ctx context.Context, local bool, projectID, self string, kinds []string, direction, afterKind, afterOther, afterDirection string, limit int) ([]RelationRow, error) {
	if limit < 1 || limit > RelationListMaxRows {
		return nil, fmt.Errorf("invalid relation list limit")
	}
	if direction != RelationDirectionOutgoing && direction != RelationDirectionIncoming && direction != RelationDirectionBoth {
		return nil, fmt.Errorf("invalid relation direction %q", direction)
	}
	probe := limit + 1
	hasCursor := afterKind != ""
	var outgoing, incoming []RelationRow
	if direction == RelationDirectionOutgoing || direction == RelationDirectionBoth {
		rows, err := d.relationDirectionPage(ctx, local, projectID, self, kinds, true, false, afterKind, afterOther, probe)
		if err != nil {
			return nil, err
		}
		outgoing = rows
	}
	if direction == RelationDirectionIncoming || direction == RelationDirectionBoth {
		rows, err := d.relationDirectionPage(ctx, local, projectID, self, kinds, false, hasCursor && afterDirection == RelationDirectionOutgoing, afterKind, afterOther, probe)
		if err != nil {
			return nil, err
		}
		incoming = rows
	}
	merged := mergeRelationRows(outgoing, incoming)
	if len(merged) > probe {
		merged = merged[:probe]
	}
	return merged, nil
}

func mergeRelationRows(outgoing, incoming []RelationRow) []RelationRow {
	merged := make([]RelationRow, 0, len(outgoing)+len(incoming))
	i, j := 0, 0
	for i < len(outgoing) && j < len(incoming) {
		if relationRowLess(outgoing[i], incoming[j]) {
			merged = append(merged, outgoing[i])
			i++
		} else {
			merged = append(merged, incoming[j])
			j++
		}
	}
	merged = append(merged, outgoing[i:]...)
	merged = append(merged, incoming[j:]...)
	return merged
}

func relationDirectionRank(direction string) int {
	if direction == RelationDirectionOutgoing {
		return 0
	}
	return 1
}

func relationRowLess(a, b RelationRow) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Other != b.Other {
		return a.Other < b.Other
	}
	return relationDirectionRank(a.Direction) < relationDirectionRank(b.Direction)
}

func (d *Databases) CountRelations(ctx context.Context, local bool) (int64, error) {
	store, err := relationStore(d, local)
	if err != nil {
		return 0, err
	}
	rows, err := store.Query(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, relationTable(local)))
	if err != nil {
		return 0, err
	}
	if len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
		return 0, fmt.Errorf("invalid relation count result")
	}
	count, ok := rows.Rows[0][0].(int64)
	if !ok {
		return 0, fmt.Errorf("invalid relation count")
	}
	return count, nil
}
