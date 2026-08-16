package mcpmanifest

var canonicalTools = []string{
	"bootstrap",
	"project_onboard",
	"session_start",
	"schema",
	"call",
	"batch",
}

// CanonicalToolNames returns the stable public MCP transport inventory.
func CanonicalToolNames() []string {
	return append([]string(nil), canonicalTools...)
}
