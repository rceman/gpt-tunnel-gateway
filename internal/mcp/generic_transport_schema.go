package mcp

import (
	"context"
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

func (s *Server) genericSchemaPublic(ctx context.Context, legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input struct {
		Session string `json:"session"`
		Path    string `json:"path"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	record, err := s.activeSession(input.Session)
	if err != nil {
		return nil, fmt.Errorf("schema session is invalid: %w", err)
	}
	if record.ProjectID == "" {
		return nil, fmt.Errorf("PROJECT_BINDING_REQUIRED: bind the session before schema discovery")
	}
	if _, err := existingSessionRoleContext(ctx, record.Role); err != nil {
		return nil, fmt.Errorf("schema session authority is invalid: %w", err)
	}
	return genericSchemaV2(schemaEntriesForSessionRole(s.genericActionRegistry(legacy), record.Role), input.Path)
}

func schemaEntriesForSessionRole(entries map[string]genericActionEntry, role string) map[string]genericActionEntry {
	filtered := make(map[string]genericActionEntry, len(entries))
	for path, entry := range entries {
		if actionAuthorityAllowsSessionRole(entry.AuthorityRole, role) {
			filtered[path] = entry
		}
	}
	return filtered
}

func genericSchemaV2(entries map[string]genericActionEntry, path string) (map[string]any, error) {
	if entry, ok := entries[path]; ok {
		return map[string]any{
			"revision": genericSchemaRevision,
			"kind":     "action",
			"path":     path,
			"contract": genericActionContractV2(entry),
		}, nil
	}
	if path == "" {
		domains := map[string]struct{}{}
		for actionPath := range entries {
			domain, _, ok := genericActionParts(actionPath)
			if ok {
				domains[domain] = struct{}{}
			}
		}
		keys := make([]string, 0, len(domains))
		for domain := range domains {
			keys = append(keys, domain)
		}
		sort.Strings(keys)
		result := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]any{"key": key})
		}
		return map[string]any{
			"revision": genericSchemaRevision,
			"kind":     "root",
			"path":     path,
			"domains":  result,
		}, nil
	}
	actions := make([]map[string]any, 0)
	for actionPath, entry := range entries {
		domain, _, ok := genericActionParts(actionPath)
		if ok && domain == path {
			actions = append(actions, map[string]any{
				"path":        actionPath,
				"description": entry.Description,
			})
		}
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("schema path %q not found; inspect schema with path=\"\"", path)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i]["path"].(string) < actions[j]["path"].(string) })
	return map[string]any{
		"revision": genericSchemaRevision,
		"kind":     "domain",
		"path":     path,
		"actions":  actions,
	}, nil
}

func genericActionContractV2(entry genericActionEntry) map[string]any {
	return map[string]any{
		"description":   entry.Description,
		"input_schema":  entry.InputSchema,
		"output_schema": entry.OutputSchema,
		"annotations": map[string]any{
			"read_only":   entry.Annotations.ReadOnlyHint,
			"destructive": entry.Annotations.DestructiveHint,
			"idempotent":  entry.Annotations.IdempotentHint,
			"open_world":  entry.Annotations.OpenWorldHint,
		},
	}
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
