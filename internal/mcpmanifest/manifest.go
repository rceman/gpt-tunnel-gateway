package mcpmanifest

var canonicalTools = []string{
	"status",
	"guide",
	"projects",
	"session_start",
	"schema",
	"call",
}

// CanonicalToolNames returns the stable public MCP transport inventory.
func CanonicalToolNames() []string {
	return append([]string(nil), canonicalTools...)
}
