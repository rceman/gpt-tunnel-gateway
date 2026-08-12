package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
)

func (s *Server) genericSchema(legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	entries := s.genericActionRegistry(legacy)
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
	if entry, ok := entries[input.Path]; ok {
		result["kind"] = "action"
		result["contract"] = genericActionContract(entry)
		return result, nil
	}
	actions := make([]map[string]any, 0)
	for path, entry := range entries {
		if domain, _, ok := genericActionParts(path); ok && domain == input.Path {
			actions = append(actions, genericActionSummary(path, entry))
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

func genericActionSummary(path string, entry genericActionEntry) map[string]any {
	domain, name, _ := genericActionParts(path)
	return map[string]any{
		"path": path, "domain": domain, "name": name, "description": entry.Description,
		"input_schema": entry.InputSchema, "output_schema": entry.OutputSchema, "annotations": entry.Annotations,
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
