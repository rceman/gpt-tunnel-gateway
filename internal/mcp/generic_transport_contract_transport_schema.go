package mcp

import (
	"strings"
)

func isRetiredPlanAction(toolName string) bool {
	return strings.HasPrefix(toolName, "plan_")
}
func sessionBoundActionPath(path string) bool {
	for _, prefix := range []string{"adr/", "agent/", "git/", "operator/", "plan/", "task/", "train/", "watcher/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
func schemaHasProperty(schema map[string]any, name string) bool {
	if schema == nil {
		return false
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		if _, exists := properties[name]; exists {
			return true
		}
		for _, child := range properties {
			if nested, ok := child.(map[string]any); ok && schemaHasProperty(nested, name) {
				return true
			}
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		for _, branch := range branches {
			if nested, ok := branch.(map[string]any); ok && schemaHasProperty(nested, name) {
				return true
			}
		}
	}
	return false
}
func withoutProjectID(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any, len(schema))
	for key, value := range schema {
		if nested, ok := value.(map[string]any); ok {
			result[key] = withoutProjectID(nested)
		} else if branches, ok := value.([]any); ok {
			copied := make([]any, len(branches))
			for i, branch := range branches {
				if nested, ok := branch.(map[string]any); ok {
					copied[i] = withoutProjectID(nested)
				} else {
					copied[i] = branch
				}
			}
			result[key] = copied
		} else {
			result[key] = value
		}
	}
	if properties, ok := result["properties"].(map[string]any); ok {
		filtered := make(map[string]any, len(properties))
		for property, value := range properties {
			if property != "project_id" {
				filtered[property] = value
			}
		}
		result["properties"] = filtered
	}
	result["required"] = removeRequiredKey(result["required"], "project_id")
	return result
}
func removeRequiredKey(value any, remove string) []string {
	result := []string{}
	for _, key := range stringList(value) {
		if key != remove {
			result = append(result, key)
		}
	}
	return result
}
func genericCallInputSchema() map[string]any {
	schema := obj(map[string]any{
		"session": str("Existing durable project-bound session identifier."),
		"action":  str("Server-owned action path; inspect schema for available actions."),
		"input":   map[string]any{"type": "object", "additionalProperties": true, "description": "Generic action input validated by the server-owned action contract."},
	}, "session", "action", "input")
	return schema
}
func sessionStartInputSchema() map[string]any {
	return obj(map[string]any{
		"project_id": str("Registered project identifier."),
		"role":       str("Server-authorized session role."),
		"label":      str("Optional bounded session label."),
		"ref":        str("Optional caller reference."),
	}, "project_id", "role")
}
func genericActionParts(path string) (string, string, bool) {
	if parts := strings.Split(path, "/"); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}
