package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/mcpmanifest"

func readOnlyAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	}
}

// canonicalToolManifest is the single stable public MCP transport inventory. Legacy
// handlers remain internal action registrations for schema/call resolution.
var canonicalToolManifest = mcpmanifest.CanonicalToolNames()

func canonicalToolNames() []string { return append([]string{}, canonicalToolManifest...) }
func additiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  false,
		OpenWorldHint:   true,
	}
}
func transportToolOutputSchema(name string) map[string]any {
	switch name {
	case "call":
		return genericCallOutputSchema()
	case "schema":
		return genericSchemaOutputSchema()
	case "status":
		return statusPublicOutputSchema()
	case "guide":
		return guidePublicOutputSchema()
	case "projects":
		return projectsPublicOutputSchema()
	default:
		return nil
	}
}

func transportToolAnnotations(name string) ToolAnnotations {
	switch name {
	case "call":
		return additiveExternalAnnotations()
	case "schema", "guide", "projects", "status":
		return readOnlyAnnotations()
	default:
		return ToolAnnotations{}
	}
}
