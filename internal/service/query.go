package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	querydsl "github.com/rceman/gpt-tunnel-gateway/internal/query"
)

type QueryRunInput struct {
	ProjectID string `json:"project_id"`
	DSL       string `json:"dsl"`
}

type QueryRunResult struct {
	Entity     string           `json:"entity"`
	Items      []map[string]any `json:"items"`
	Count      int              `json:"count"`
	NextCursor string           `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
}

func (s *Service) QueryRun(ctx context.Context, in QueryRunInput) (QueryRunResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return QueryRunResult{}, err
	}
	q, err := querydsl.Parse(in.DSL)
	if err != nil {
		return QueryRunResult{}, err
	}
	descriptor, err := queryDescriptor(q.Entity)
	if err != nil {
		return QueryRunResult{}, err
	}
	if err := validateQueryFields(q, descriptor); err != nil {
		return QueryRunResult{}, err
	}
	rows, err := s.querySharedRows(ctx, in.ProjectID, descriptor)
	if err != nil {
		return QueryRunResult{}, err
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, value := range rows {
		if queryMatches(value, q, descriptor) {
			filtered = append(filtered, value)
		}
	}
	rows = filtered
	orderField := q.Order.Field
	if orderField == "" {
		orderField = descriptor.Default[0]
	}
	sort.SliceStable(rows, func(i, j int) bool { return compareRows(rows[i], rows[j], orderField, q.Order.Desc) })
	limit, err := pagination.Limit(q.Limit, s.Config.MaxListItems)
	if err != nil {
		return QueryRunResult{}, err
	}
	page, info, err := pagination.Page("query:"+in.DSL, rows, limit, q.Cursor, func(row map[string]any) string { return queryRowKey(row, orderField) })
	if err != nil {
		return QueryRunResult{}, err
	}
	result := QueryRunResult{
		Entity:     q.Entity,
		Count:      len(rows),
		HasMore:    info.HasMore,
		NextCursor: info.NextCursor,
		Items:      projectRows(page, q.Select, descriptor.Default),
	}
	if q.Count {
		result.Items = []map[string]any{}
	}
	return result, nil
}

// querySharedRows is the sole authority for the public collection query. The
// Message family intentionally has no Shared projection; it therefore fails
// closed instead of falling back to the Hub entity registry.
func (s *Service) querySharedRows(ctx context.Context, projectID string, descriptor entity.Descriptor) ([]map[string]any, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return nil, err
	}
	entityType := ""
	switch descriptor.Family {
	case entity.TaskFamily:
		entityType = "task"
	case entity.ADRFamily:
		entityType = "adr"
	case entity.RuleFamily:
		entityType = "rule"
	case entity.TrainFamily:
		entityType = "train"
	case entity.JournalFamily:
		entityType = "journal"
	case entity.MessageFamily:
		return nil, fmt.Errorf("Shared Message authority is unavailable")
	default:
		return nil, fmt.Errorf("Shared query authority is unavailable for %q", descriptor.Name)
	}
	entities, err := s.sharedProjectEntities(ctx, entityType, projectID)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(entities))
	for _, shared := range entities {
		value, err := decodeQueryObject(shared.Payload)
		if err != nil {
			return nil, err
		}
		if value["id"] == nil {
			value["id"] = shared.ID
		}
		rows = append(rows, value)
	}
	return rows, nil
}

func queryDescriptor(name string) (entity.Descriptor, error) {
	for _, descriptor := range entity.Descriptors() {
		if strings.ToLower(descriptor.Name) == strings.ToLower(name) {
			return descriptor, nil
		}
	}
	return entity.Descriptor{}, fmt.Errorf("query entity %q is not supported", name)
}

func validateQueryFields(q querydsl.Query, descriptor entity.Descriptor) error {
	allowed := func(field string, fields []string) bool {
		for _, item := range fields {
			if item == field {
				return true
			}
		}
		return false
	}
	for _, field := range q.Select {
		if !allowed(field, descriptor.Fields) {
			return fmt.Errorf("query field %q is not selectable", field)
		}
	}
	for _, filter := range q.Filters {
		if filter.Field != "" && !allowed(filter.Field, descriptor.Filterable) {
			return fmt.Errorf("query field %q is not filterable", filter.Field)
		}
	}
	if q.Order.Field != "" && !allowed(q.Order.Field, descriptor.Sortable) {
		return fmt.Errorf("query field %q is not sortable", q.Order.Field)
	}
	return nil
}

func queryMatches(row map[string]any, q querydsl.Query, descriptor entity.Descriptor) bool {
	for _, filter := range q.Filters {
		if filter.Field == "" {
			if !containsSearch(row, descriptor.Searchable, filter.Values[0]) {
				return false
			}
			continue
		}
		actual := queryValue(row[filter.Field])
		matched := false
		for _, wanted := range filter.Values {
			if (filter.Op == "contains" && strings.Contains(strings.ToLower(actual), strings.ToLower(wanted))) || (filter.Op == "=" && actual == wanted) || (filter.Op == "in" && actual == wanted) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func containsSearch(row map[string]any, fields []string, wanted string) bool {
	for _, field := range fields {
		if strings.Contains(strings.ToLower(queryValue(row[field])), strings.ToLower(wanted)) {
			return true
		}
	}
	return false
}
func queryValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
	}
}
func compareRows(left, right map[string]any, field string, desc bool) bool {
	l, r := queryValue(left[field]), queryValue(right[field])
	if l == r {
		return queryValue(left["id"]) < queryValue(right["id"])
	}
	if desc {
		return l > r
	}
	return l < r
}
func queryRowKey(row map[string]any, field string) string {
	return queryValue(row[field]) + "\x00" + queryValue(row["id"])
}
func projectRows(rows []map[string]any, selected, defaults []string) []map[string]any {
	fields := selected
	if len(fields) == 0 {
		fields = defaults
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		projected := map[string]any{}
		for _, field := range fields {
			if value, ok := row[field]; ok {
				projected[field] = value
			}
		}
		result = append(result, projected)
	}
	return result
}

func decodeQueryObject(data []byte) (map[string]any, error) {
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, fmt.Errorf("query record is not an object")
	}
	return value, nil
}
