package mcpmanifest

var canonicalTools = []string{
	"status",
	"session_start",
	"schema",
	"call",
	"batch",
}

// CanonicalToolNames returns the stable public MCP transport inventory.
func CanonicalToolNames() []string {
	return append([]string(nil), canonicalTools...)
}
