package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) genericSchemaPublic(ctx context.Context, legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input struct {
		Session string `json:"session"`
		Path    string `json:"path"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	entries := s.genericActionRegistry(legacy)
	if input.Session == "" {
		return genericSchemaV2(entries, input.Path)
	}
	record, err := s.activeSession(input.Session)
	if err != nil {
		return nil, fmt.Errorf("schema session is invalid: durable Session authentication failed: %w", err)
	}
	ctx, err = s.authenticateSession(ctx, entries, record, input.Path)
	if err != nil {
		return nil, fmt.Errorf("schema session authority is invalid: %w", err)
	}
	if record.ProjectID == "" && record.Role != durableSession.RoleAdmin {
		return nil, fmt.Errorf("PROJECT_BINDING_REQUIRED: bind the session before schema discovery")
	}
	if record.Role != durableSession.RoleAdmin {
		if _, err := existingSessionRoleContext(ctx, record.Role); err != nil {
			return nil, fmt.Errorf("schema session authority is invalid: %w", err)
		}
	}
	return genericSchemaV2(schemaEntriesForSessionRole(entries, record.Role), input.Path)
}

func schemaEntriesForSessionRole(entries map[string]genericActionEntry, role string) map[string]genericActionEntry {
	if role == durableSession.RoleAdmin {
		available := make(map[string]genericActionEntry)
		for path, entry := range entries {
			if strings.HasPrefix(path, "admin/") {
				available[path] = entry
			}
		}
		return available
	}
	if !actionAuthorityAllowsSessionRole("", role) {
		return map[string]genericActionEntry{}
	}
	available := make(map[string]genericActionEntry, len(entries))
	for path, entry := range entries {
		available[path] = entry
	}
	return available
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
				"description": entry.Contract.Description,
				"input":       entry.Contract.CompactInputSchema(),
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
		"description":   entry.Contract.Description,
		"input_schema":  entry.Contract.Input.JSONSchema(),
		"output_schema": entry.Contract.Output.JSONSchema(),
		"annotations": map[string]any{
			"read_only":   entry.Annotations.ReadOnlyHint,
			"destructive": entry.Annotations.DestructiveHint,
			"idempotent":  entry.Annotations.IdempotentHint,
			"open_world":  entry.Annotations.OpenWorldHint,
		},
	}
}
