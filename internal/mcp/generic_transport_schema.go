package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func (s *Server) genericSchema(legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input struct {
		Path   string `json:"path"`
		Detail bool   `json:"detail,omitempty"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	entries := s.genericActionRegistry(legacy)
	if entry, ok := entries[input.Path]; ok {
		return map[string]any{
			"revision": genericSchemaRevision, "path": input.Path, "kind": "action",
			"domains": []string{}, "actions": []map[string]any{}, "contract": genericActionContract(entry),
		}, nil
	}
	if input.Path == "query" || strings.HasPrefix(input.Path, "query/") {
		contract, err := querySchema(input.Path)
		if err != nil {
			return nil, err
		}
		kind := "query"
		if input.Path != "query" {
			kind = "query_entity"
		}
		return map[string]any{"revision": genericSchemaRevision, "path": input.Path, "kind": kind, "domains": []string{}, "actions": []map[string]any{}, "contract": contract}, nil
	}
	result := map[string]any{
		"revision": genericSchemaRevision, "path": input.Path, "kind": "root",
		"domains": []string{}, "actions": []map[string]any{}, "contract": map[string]any{},
	}
	if input.Path == "" {
		domains := map[string]bool{}
		for path := range entries {
			if domain, _, ok := genericActionParts(path); ok {
				domains[domain] = true
			}
		}
		result["domains"] = sortedKeys(domains)
		return result, nil
	}
	actions := make([]map[string]any, 0)
	for path, entry := range entries {
		if domain, _, ok := genericActionParts(path); ok && domain == input.Path {
			if input.Detail {
				actions = append(actions, genericActionSummary(path, entry))
			} else {
				actions = append(actions, genericActionCompactSummary(path, entry))
			}
		}
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("schema path %q not found; inspect schema with path=\"\"", input.Path)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i]["path"].(string) < actions[j]["path"].(string) })
	result["kind"] = "domain"
	result["actions"] = actions
	return result, nil
}

func genericActionContract(entry genericActionEntry) map[string]any {
	return genericActionSummary(entry.Path, entry)
}

func genericActionCompactSummary(path string, entry genericActionEntry) map[string]any {
	domain, name, _ := genericActionParts(path)
	return map[string]any{
		"path": path, "domain": domain, "name": name, "description": entry.Description,
		"annotations": entry.Annotations, "session_required": entry.SessionRequired,
	}
}

func genericActionSummary(path string, entry genericActionEntry) map[string]any {
	domain, name, _ := genericActionParts(path)
	return map[string]any{
		"path": path, "domain": domain, "name": name, "description": entry.Description,
		"input_schema": entry.InputSchema, "output_schema": entry.OutputSchema, "annotations": entry.Annotations,
		"session_required": entry.SessionRequired,
	}
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
