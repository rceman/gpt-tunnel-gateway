package sqlitestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
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
