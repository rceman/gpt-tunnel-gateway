package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

// sharedLifecycleDefinition is the single registry for Shared durable-entity
// storage mechanics. Entity-specific service code supplies validated payloads;
// this registry owns state, allocation, history, and outbox composition.
// Identifiers in this registry are trusted SQL identifiers, never caller input.
type sharedLifecycleDefinition struct {
	EntityType            string
	StateTable            string
	SequenceTable         string
	SequenceEntityColumn  string
	SequenceCodeColumn    string
	SequenceNumberColumn  string
	IDToken               string
	HistoryTable          string
	HistoryEntityColumn   string
	HistoryIDColumn       string
	SearchFields          []string
	FilterFields          []string
	DefaultCreateStatus   string
	AllowedCreateStatuses []string
	AllowedStatuses       []string
	AllowedTransitions    map[string][]string
}

var sharedLifecycleRegistry = map[string]sharedLifecycleDefinition{
	"task": {
		EntityType:            "task",
		StateTable:            "shared_tasks",
		SequenceTable:         "shared_entity_sequences",
		SequenceEntityColumn:  "entity_type",
		SequenceCodeColumn:    "project_code",
		SequenceNumberColumn:  "next_number",
		IDToken:               "TSK",
		HistoryTable:          "shared_entity_revisions",
		HistoryEntityColumn:   "entity_type",
		HistoryIDColumn:       "entity_id",
		SearchFields:          []string{"id", "title", "summary", "objective", "status", "type", "priority", "adr_relation"},
		FilterFields:          []string{"status", "type"},
		DefaultCreateStatus:   "planned",
		AllowedCreateStatuses: []string{"planned"},
		AllowedStatuses:       []string{"planned", "ready", "done", "archived"},
		AllowedTransitions:    map[string][]string{"planned": {"ready", "done", "archived"}, "ready": {"done", "archived"}},
	},
	"train": {
		EntityType:   "train",
		StateTable:   "shared_trains",
		SearchFields: []string{"id", "title", "summary", "description", "status"},
		FilterFields: []string{"status"},
	},
	"adr": {
		EntityType:            "adr",
		StateTable:            "shared_adrs",
		SequenceTable:         "shared_entity_sequences",
		SequenceEntityColumn:  "entity_type",
		SequenceCodeColumn:    "project_code",
		SequenceNumberColumn:  "next_number",
		IDToken:               "ADR",
		HistoryTable:          "shared_entity_revisions",
		HistoryEntityColumn:   "entity_type",
		HistoryIDColumn:       "entity_id",
		SearchFields:          []string{"id", "title", "status", "context", "decision", "consequences", "supersedes"},
		FilterFields:          []string{"status"},
		DefaultCreateStatus:   "proposed",
		AllowedCreateStatuses: []string{"proposed"},
		AllowedStatuses:       []string{"proposed", "accepted", "superseded", "archived"},
		AllowedTransitions:    map[string][]string{"proposed": {"accepted", "archived"}, "accepted": {"superseded", "archived"}, "superseded": {"archived"}},
	},
	"rule": {
		EntityType:   "rule",
		StateTable:   "shared_rules",
		SearchFields: []string{"id", "title", "summary", "description", "status"},
		FilterFields: []string{"status"},
	},
	"journal": {
		EntityType:   "journal",
		StateTable:   "shared_journals",
		SearchFields: []string{"id", "title", "summary", "description", "status", "kind"},
		FilterFields: []string{"status", "kind"},
	},
	"project_configuration": {
		EntityType:   "project_configuration",
		StateTable:   "shared_project_configurations",
		SearchFields: []string{"id", "name", "description", "status"},
		FilterFields: []string{"status"},
	},
}

// SharedLifecycleQuery is the entity-neutral query contract. The registry
// supplies searchable/filterable fields; callers provide only normalized
// values and receive the same bounded, deterministic page for every entity.
type SharedLifecycleQuery struct {
	EntityType        string
	ProjectID         string
	Text              string
	Filters           map[string]string
	IncludeArchived   bool
	ExcludeSuperseded bool
	Limit             int
	Cursor            string
}

type SharedLifecycleQueryPage struct {
	Entities   []SharedEntity
	NextCursor string
	HasMore    bool
	CursorKind string
}

const SharedLifecycleQueryMaxRows = 256

// QuerySharedLifecycle executes the descriptor-approved predicates at the
// SQLite boundary. The database performs keyset ordering and reads only
// limit+1 matching rows; no client-side sparse scan or full materialization is
// used.
func (d *Databases) QuerySharedLifecycle(ctx context.Context, query SharedLifecycleQuery) (SharedLifecycleQueryPage, error) {
	definition, ok := sharedLifecycle(query.EntityType)
	if !ok {
		return SharedLifecycleQueryPage{}, fmt.Errorf("unsupported shared entity type %q", query.EntityType)
	}
	if d == nil || d.Shared == nil {
		return SharedLifecycleQueryPage{}, fmt.Errorf("shared store is unavailable")
	}
	if query.ProjectID == "" || query.Limit < 1 || query.Limit > SharedLifecycleQueryMaxRows {
		return SharedLifecycleQueryPage{}, fmt.Errorf("invalid shared %s query", query.EntityType)
	}
	for field := range query.Filters {
		if !containsString(definition.FilterFields, field) {
			return SharedLifecycleQueryPage{}, fmt.Errorf("unsupported %s filter %q", query.EntityType, field)
		}
	}
	kindBytes, err := json.Marshal(struct {
		EntityType        string            `json:"entity_type"`
		ProjectID         string            `json:"project_id"`
		Text              string            `json:"text"`
		Filters           map[string]string `json:"filters"`
		Archived          bool              `json:"archived"`
		ExcludeSuperseded bool              `json:"exclude_superseded"`
	}{query.EntityType, query.ProjectID, strings.ToLower(strings.TrimSpace(query.Text)), query.Filters, query.IncludeArchived, query.ExcludeSuperseded})
	if err != nil {
		return SharedLifecycleQueryPage{}, err
	}
	kind := "shared_lifecycle:" + string(kindBytes)
	afterID := ""
	if query.Cursor != "" {
		afterID, err = pagination.DecodeOpaqueKeyset(query.Cursor, kind)
		if err != nil {
			return SharedLifecycleQueryPage{}, err
		}
	}
	entities, err := d.querySharedLifecycleRows(ctx, definition, query, afterID, query.Limit+1)
	if err != nil {
		return SharedLifecycleQueryPage{}, err
	}
	result := SharedLifecycleQueryPage{
		Entities:   entities,
		CursorKind: kind,
	}
	if len(entities) > query.Limit {
		result.Entities = entities[:query.Limit]
		result.HasMore = true
		result.NextCursor = pagination.EncodeOpaqueKeyset(kind, result.Entities[len(result.Entities)-1].ID)
	}
	return result, nil
}

func (d *Databases) querySharedLifecycleRows(ctx context.Context, definition sharedLifecycleDefinition, query SharedLifecycleQuery, afterID string, limit int) ([]SharedEntity, error) {
	if limit < 1 || limit > SharedLifecycleQueryMaxRows+1 {
		return nil, fmt.Errorf("invalid shared lifecycle query limit")
	}
	where := []string{"json_extract(payload, '$.project_id') = ?"}
	args := []any{query.ProjectID}
	if !query.IncludeArchived {
		where = append(where, "COALESCE(json_extract(payload, '$.status'), '') NOT IN (?, ?)")
		args = append(args, "archived", "superseded")
	} else if query.ExcludeSuperseded {
		where = append(where, "COALESCE(json_extract(payload, '$.status'), '') != ?")
		args = append(args, "superseded")
	}
	fields := make([]string, 0, len(query.Filters))
	for field := range query.Filters {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		where = append(where, fmt.Sprintf("json_extract(payload, '$.%s') = ?", field))
		args = append(args, query.Filters[field])
	}
	if text := strings.ToLower(strings.TrimSpace(query.Text)); text != "" {
		terms := make([]string, 0, len(definition.SearchFields))
		for _, field := range definition.SearchFields {
			terms = append(terms, fmt.Sprintf("lower(COALESCE(json_extract(payload, '$.%s'), '')) LIKE ?", field))
			args = append(args, "%"+text+"%")
		}
		where = append(where, "("+strings.Join(terms, " OR ")+")")
	}
	if afterID != "" {
		where = append(where, "id > ?")
		args = append(args, afterID)
	}
	args = append(args, limit)
	querySQL := fmt.Sprintf("SELECT id,revision,payload,updated_at FROM %s WHERE %s ORDER BY id LIMIT ?", definition.StateTable, strings.Join(where, " AND "))
	rows, err := d.Shared.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	return decodeSharedEntityRows(query.EntityType, rows.Rows)
}

func decodeSharedEntityRows(entityType string, rows [][]any) ([]SharedEntity, error) {
	entities := make([]SharedEntity, 0, len(rows))
	for _, row := range rows {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid shared %s row", entityType)
		}
		id, idOK := row[0].(string)
		revision, revisionOK := row[1].(int64)
		payload, payloadOK := row[2].([]byte)
		updatedAt, updatedOK := row[3].(string)
		if !idOK || !revisionOK || !payloadOK || !updatedOK {
			return nil, fmt.Errorf("invalid shared %s row", entityType)
		}
		entities = append(entities, SharedEntity{
			ID:        id,
			Revision:  revision,
			Payload:   append([]byte(nil), payload...),
			UpdatedAt: updatedAt,
		})
	}
	return entities, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func isArchivedLifecycle(fields map[string]any) bool {
	status, _ := fields["status"].(string)
	return status == "archived" || status == "superseded"
}

func matchesLifecycleFilters(fields map[string]any, filters map[string]string) bool {
	for field, expected := range filters {
		actual, _ := fields[field].(string)
		if actual != expected {
			return false
		}
	}
	return true
}

func matchesLifecycleText(fields map[string]any, searchFields []string, text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return true
	}
	for _, field := range searchFields {
		value, _ := fields[field].(string)
		if strings.Contains(strings.ToLower(value), text) {
			return true
		}
	}
	return false
}

func sharedLifecycle(entityType string) (sharedLifecycleDefinition, bool) {
	definition, ok := sharedLifecycleRegistry[entityType]
	return definition, ok
}

func sharedProjectionTable(entityType string) (string, bool) {
	if definition, ok := sharedLifecycle(entityType); ok {
		return definition.StateTable, true
	}
	table, ok := sharedProjectionTables[entityType]
	return table, ok
}

// SharedLifecycleCreate is the generic local-first create unit. If the
// registered entity has an allocator, its sequence advance, state row,
// optional history row, and outbox intent are committed atomically.
