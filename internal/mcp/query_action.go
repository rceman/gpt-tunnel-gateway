package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func queryGenericAction(s *Server) genericActionEntry {
	input := obj(map[string]any{"dsl": str("Collection-only V1 query DSL; use <entity>.list(...).")}, "dsl")
	execution := obj(map[string]any{"dsl": str("Collection-only V1 query DSL."), "project_id": str("Session-bound project.")}, "dsl", "project_id")
	return genericActionEntry{GenericAction: GenericAction{
		Path:                 "query/run",
		Description:          "Execute one bounded read-only collection query.",
		InputSchema:          input,
		OutputSchema:         closedOutput(map[string]any{"entity": outputString(), "items": outputArray(map[string]any{"type": "object", "additionalProperties": true}), "count": outputInteger(), "next_cursor": outputString(), "has_more": outputBoolean()}, "entity", "items", "count", "has_more"),
		Annotations:          readOnlyAnnotations(),
		SessionBound:         true,
		ExecutionInputSchema: execution,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input service.QueryRunInput
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.QueryRun(ctx, input)
		},
	}}
}

func querySchema(path string) (map[string]any, error) {
	if path == "query" {
		entities := []string{}
		for _, descriptor := range entity.Descriptors() {
			entities = append(entities, strings.ToLower(descriptor.Name))
		}
		sort.Strings(entities)
		return map[string]any{"grammar": "query-v1", "entity_syntax": "<entity>.list(...)", "entities": entities, "read": "rejected; use typed <entity>/read"}, nil
	}
	name := strings.TrimPrefix(path, "query/")
	if name == path || strings.Contains(name, "/") {
		return nil, fmt.Errorf("query schema path %q not found", path)
	}
	for _, descriptor := range entity.Descriptors() {
		if strings.EqualFold(name, descriptor.Name) {
			return map[string]any{"entity": strings.ToLower(descriptor.Name), "default_projection": descriptor.Default, "fields": descriptor.Fields, "searchable": descriptor.Searchable, "filterable": descriptor.Filterable, "sortable": descriptor.Sortable, "operators": descriptor.Operators, "default_order": descriptor.Order, "max_limit": 100}, nil
		}
	}
	return nil, fmt.Errorf("query schema path %q not found", path)
}
